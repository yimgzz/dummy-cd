package provider

import (
	"context"
	"errors"
	"github.com/go-git/go-git/v5/plumbing"
	log "github.com/sirupsen/logrus"
	"github.com/yimgzz/dummy-cd/server/pkg/util"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"strings"
	"sync"
)

type Resource struct {
	group    string
	version  string
	resource string
	filePath string
	obj      *unstructured.Unstructured
}

type RawProvider struct {
	appName       *string
	resourcePath  *string
	namespace     *string
	resourceFiles *[]string
	appRevision   *plumbing.Hash
	dynamicClient *dynamic.DynamicClient
	clientSet     *kubernetes.Clientset
	resources     []*Resource
	labels        map[string]string
	mutex         *sync.Mutex
	ctx           context.Context
	kubeConfig    *rest.Config
}

func NewUnstructuredResource(path string) *Resource {
	resource := make(map[string]interface{})

	file := util.ReadFile(&path)

	err := yaml.Unmarshal(*file, &resource)

	if err != nil {
		log.Error(err)
		return nil
	}

	if len(resource) == 0 {
		log.Errorf("empty resource found, skipping %s", path)
		return nil
	}

	obj := &unstructured.Unstructured{Object: resource}

	apiVersionTokens := strings.Split(obj.GetAPIVersion(), "/")

	var version string
	var group string
	if len(apiVersionTokens) == 1 {
		version = apiVersionTokens[0]
		group = ""
	} else {
		group = apiVersionTokens[0]
		version = apiVersionTokens[1]
	}

	return &Resource{
		filePath: path,
		group:    group,
		version:  version,
		resource: strings.ToLower(obj.GetKind() + "s"),
		obj:      obj,
	}

}

func NewUnstructuredResources(files []string) []*Resource {
	var resources []*Resource

	for _, path := range files {
		resource := NewUnstructuredResource(path)

		if resource == nil {
			continue
		}

		resources = append(resources, resource)
	}

	return resources
}

func NewRawProvider(ctx context.Context, appName *string, resourcePath *string,
	namespace *string, restKubeConfig *rest.Config, appRevision *plumbing.Hash) (*RawProvider, error) {

	var err error

	var ns string
	if len(*namespace) == 0 {
		ns = metav1.NamespaceDefault
	} else {
		ns = *namespace
	}

	rawProvider := RawProvider{
		appName:      appName,
		resourcePath: resourcePath,
		namespace:    &ns,
		appRevision:  appRevision,
		mutex:        new(sync.Mutex),
		ctx:          ctx,
		kubeConfig:   restKubeConfig,
	}

	rawProvider.clientSet, err = kubernetes.NewForConfig(restKubeConfig)

	if err != nil {
		rawProvider.logWithFields().Error(err)
		return nil, err
	}

	_, err = rawProvider.clientSet.CoreV1().Namespaces().Get(rawProvider.ctx, ns, metav1.GetOptions{})

	if err != nil {
		if k8sErrors.IsNotFound(err) {
			_, err = rawProvider.clientSet.CoreV1().Namespaces().Create(
				rawProvider.ctx,
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}},
				metav1.CreateOptions{})

			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	rawProvider.dynamicClient, err = dynamic.NewForConfig(restKubeConfig)

	if err != nil {
		rawProvider.logWithFields().Error(err)
		return nil, err
	}

	resourceFiles, err := rawProvider.getResourceFiles()

	if err != nil {
		return nil, err
	}

	if len(resourceFiles) == 0 {
		return nil, errors.New("no one resource file found")
	}

	rawProvider.resources = NewUnstructuredResources(resourceFiles)

	//if len(rawProvider.resources) == 0 {
	//	return nil, errors.New("no one resource appended")
	//}

	rawProvider.labels = map[string]string{
		"dummy.cd/app": *appName,
	}

	return &rawProvider, nil
}

func (r *RawProvider) logWithFields() *log.Entry {
	return log.WithFields(log.Fields{"app": *r.appName, "revision": r.appRevision.String()})
}

func (r *RawProvider) getResourceFiles() ([]string, error) {
	resourceFiles := util.FindFilesWithRegex(r.resourcePath, "^.+\\.(yaml|yml)$")

	//if len(resourceFiles) == 0 {
	//	return nil, errors.New("no one resource appended")
	//}

	resourceFiles = util.GetFilesFullPath(r.resourcePath, &resourceFiles)

	return resourceFiles, nil
}

func (r *RawProvider) cleanResources(cleanAll bool) error {
	defer r.mutex.Unlock()

	if r.appRevision.IsZero() {
		return nil
	}

	apiGroupList, err := r.clientSet.ServerGroups()

	if err != nil {
		r.logWithFields().Error(err)
		return err
	}

	labelAppNameRequirement, err := labels.NewRequirement("dummy.cd/app", selection.Equals, []string{*r.appName})

	if err != nil {
		r.logWithFields().Debug(err)
		return err
	}

	labelsSelector := labels.NewSelector()
	labelsSelector = labelsSelector.Add(*labelAppNameRequirement)

	if !cleanAll {
		labelAppRevisionRequirement, err := labels.NewRequirement("dummy.cd/revision", selection.NotEquals, []string{r.appRevision.String()})

		if err != nil {
			r.logWithFields().Debug(err)
			return err
		}

		labelsSelector = labelsSelector.Add(*labelAppRevisionRequirement)
	}

	for _, apiGroup := range apiGroupList.Groups {
		for _, apiGroupVersion := range apiGroup.Versions {
			apiResourceList, err := r.clientSet.ServerResourcesForGroupVersion(apiGroupVersion.GroupVersion)

			if err != nil {
				r.logWithFields().Debug(err)
				continue
			}

			blocker := make(chan struct{}, 10)

			for _, apiResource := range apiResourceList.APIResources {

				blocker <- struct{}{}
				go func(apiGroupName string, apiGroupVersion string, apiResourceName string) {

					r.logWithFields().Tracef("run cleanup task on %s: %s", apiGroupVersion, apiResourceName)

					remoteResourceList, err := r.dynamicClient.Resource(schema.GroupVersionResource{
						Group:    apiGroupName,
						Version:  apiGroupVersion,
						Resource: apiResourceName,
					}).Namespace(*r.namespace).List(r.ctx, metav1.ListOptions{
						LabelSelector: labelsSelector.String(),
					})

					if err != nil {
						r.logWithFields().Tracef("%s: %s: %s", apiGroupVersion, apiResourceName, err)
						<-blocker
						return
					}

					r.logWithFields().Tracef("done cleanup task on %s: %s", apiGroupVersion, apiResourceName)

					for _, remoteResource := range remoteResourceList.Items {
						r.logWithFields().Debugf("deleting %s", remoteResource)

						propagationPolicy := metav1.DeletePropagationForeground

						err := r.dynamicClient.Resource(schema.GroupVersionResource{
							Group:    apiGroupName,
							Version:  apiGroupVersion,
							Resource: apiResourceName,
						}).Namespace(*r.namespace).Delete(r.ctx, remoteResource.GetName(), metav1.DeleteOptions{
							PropagationPolicy: &propagationPolicy,
						})

						if err != nil {
							r.logWithFields().Debugf("error: %s: %s", err, remoteResource)
						} else {
							r.logWithFields().Debugf("deleted %s", remoteResource)
						}
					}
					<-blocker
				}(apiGroup.Name, apiGroupVersion.Version, apiResource.Name)
			}
		}
	}

	return nil
}

func (r *Resource) Delivery(p *RawProvider, wg *sync.WaitGroup) {
	defer wg.Done()

	remoteResource, err := p.dynamicClient.Resource(schema.GroupVersionResource{
		Group:    r.group,
		Version:  r.version,
		Resource: r.resource,
	}).Namespace(*p.namespace).Get(p.ctx, r.obj.GetName(), metav1.GetOptions{})

	r.obj.SetLabels(p.labels)

	if err != nil {
		if k8sErrors.IsNotFound(err) {
			_, err := p.dynamicClient.Resource(schema.GroupVersionResource{
				Group:    r.group,
				Version:  r.version,
				Resource: r.resource,
			}).Namespace(*p.namespace).Create(p.ctx, r.obj, metav1.CreateOptions{})

			if err != nil {
				p.logWithFields().Errorf("%s: %+v", err, r)
				return
			}

			p.logWithFields().Debugf("resource created %+v", r)
			return
		} else {
			p.logWithFields().Error(err)
			return
		}
	}

	remoteLabels := remoteResource.GetLabels()

	_, exist := remoteLabels["dummy.cd/revision"]

	if !exist {
		p.logWithFields().Errorf("label dummy.cd/revision not exist on %+v", r)
		return
	}

	if p.labels["dummy.cd/revision"] != remoteLabels["dummy.cd/revision"] {
		r.obj.SetResourceVersion(remoteResource.GetResourceVersion())

		_, err = p.dynamicClient.Resource(schema.GroupVersionResource{
			Group:    r.group,
			Version:  r.version,
			Resource: r.resource,
		}).Namespace(*p.namespace).
			Update(p.ctx, r.obj, metav1.UpdateOptions{})

		if err != nil {
			p.logWithFields().Errorf("error while updating %+v", r)
		}

	} else {
		p.logWithFields().Debugf(
			"revision already applied for %+v", r,
		)
	}
}

func (r *RawProvider) Uninstall() error {
	for {
		if r.mutex.TryLock() {
			err := r.cleanResources(true)

			return err
		} else {
			r.logWithFields().Debug("pending all resources removal")
		}
	}
}

func (r *RawProvider) Delivery() error {
	_, revisionLabelExist := r.labels["dummy.cd/revision"]

	if revisionLabelExist {
		resourceFiles, err := r.getResourceFiles()

		if err != nil {
			r.logWithFields().Error(err)
			return err
		}

		r.resources = NewUnstructuredResources(resourceFiles)
	}

	r.labels["dummy.cd/revision"] = r.appRevision.String()

	var wg sync.WaitGroup

	for _, resource := range r.resources {
		wg.Add(1)
		go resource.Delivery(r, &wg)
	}

	wg.Wait()

	r.logWithFields().Debug("done apply resources")

	if r.mutex.TryLock() {
		go func() {
			err := r.cleanResources(false)

			if err != nil {
				r.logWithFields().Error(err)
			}

			r.logWithFields().Debug("cleanup task is done")
		}()
	} else {
		r.logWithFields().Debug("skip running cleanup, task already in process")
	}

	return nil
}
