package provider

import (
	"flag"
	"github.com/go-git/go-git/v5/plumbing"
	log "github.com/sirupsen/logrus"
	"github.com/yimgzz/dummy-cd/server/pkg/util"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/cli/values"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/release"
	"io"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"os"
	"path"
	"reflect"
	"sync"
	"time"
)

var (
	helmDebug = flag.Bool("helm-debug", true, "enable debug on helm client")
)

type HelmActionOptions struct {
	CheckValuesEqual bool
	ReInstallRelease bool
	CreateNamespace  bool
	Atomic           bool
	IncludeCRDs      bool
	actionTimeout    time.Duration
}

type HelmProvider struct {
	cfg           *action.Configuration
	chart         *chart.Chart
	settings      *cli.EnvSettings
	manager       *downloader.Manager
	ActionOptions *HelmActionOptions
	appRevision   *plumbing.Hash
	releaseName   *string
	chartPath     *string
	ValueFiles    []string
	chartValues   map[string]interface{}
	namespace     *string
	mutex         *sync.Mutex
}

func NewHelmKubernetesConfig(restKubeConfig *rest.Config, namespace *string) *genericclioptions.ConfigFlags {
	helmKubeConfig := genericclioptions.NewConfigFlags(false)

	helmKubeConfig.APIServer = &restKubeConfig.Host
	helmKubeConfig.BearerToken = &restKubeConfig.BearerToken
	helmKubeConfig.CAFile = &restKubeConfig.CAFile
	helmKubeConfig.CertFile = &restKubeConfig.CertFile
	helmKubeConfig.KeyFile = &restKubeConfig.KeyFile
	helmKubeConfig.Namespace = namespace
	helmKubeConfig.Insecure = &restKubeConfig.Insecure

	return helmKubeConfig
}

func NewHelmSettings(namespace *string) *cli.EnvSettings {
	settings := cli.New()

	settings.Debug = true
	settings.SetNamespace(*namespace)
	settings.RepositoryCache = path.Join(*util.GetUserHome(), ".cache", "helm", "repository")

	return settings
}

func NewHelmManager(chartPath *string, settings *cli.EnvSettings) (*downloader.Manager, error) {
	manager := &downloader.Manager{
		Out:             io.Discard,
		ChartPath:       *chartPath,
		Debug:           *helmDebug,
		Getters:         getter.All(settings),
		RepositoryCache: settings.RepositoryCache,
	}

	err := manager.Update()

	return manager, err
}

func NewHelmChart(manager *downloader.Manager, chartPath *string) (*chart.Chart, error) {
	if err := manager.Update(); err != nil {
		log.Error(err)
		return nil, err
	}

	helmChart, err := loader.Load(*chartPath)

	if err != nil {
		log.Error(err)
		return nil, err
	}

	return helmChart, nil

}

func NewHelmChartValues(settings *cli.EnvSettings, valueFiles *[]string) (map[string]interface{}, error) {
	options := &values.Options{
		ValueFiles: *valueFiles,
	}

	chartValues, err := options.MergeValues(getter.All(settings))

	if err != nil {
		log.Error(err)
		return nil, err
	}

	return chartValues, nil

}

func NewHelmProvider(releaseName *string, chartPath *string, namespace *string,
	helm *HelmProvider, restKubeConfig *rest.Config, appRevision *plumbing.Hash) (*HelmProvider, error) {

	var err error

	helm.cfg = new(action.Configuration)
	helm.releaseName = releaseName
	helm.chartPath = chartPath
	helm.namespace = namespace

	helm.ActionOptions.actionTimeout = 360 * time.Second

	helm.settings = NewHelmSettings(namespace)
	helm.settings.SetNamespace(*namespace)

	helm.manager, err = NewHelmManager(chartPath, helm.settings)

	if err != nil {
		helm.logWithFields().Error(err)
		return nil, err
	}

	if err := helm.cfg.Init(NewHelmKubernetesConfig(restKubeConfig, namespace), *namespace, os.Getenv("HELM_DRIVER"), log.Debugf); err != nil {
		helm.logWithFields().Error(err)
		return nil, err
	}

	helm.appRevision = appRevision
	helm.ValueFiles = util.GetFilesFullPath(helm.chartPath, &helm.ValueFiles)

	helm.chart, err = NewHelmChart(helm.manager, helm.chartPath)

	if err != nil {
		helm.logWithFields().Error(err)
		return nil, err
	}

	return helm, nil
}

func (h *HelmProvider) logWithFields() *log.Entry {
	return log.WithFields(log.Fields{"app": *h.releaseName, "revision": h.appRevision.String()})
}

func (h *HelmProvider) loadHelmChartWithValues() error {
	var err error

	if err := h.manager.Update(); err != nil {
		h.logWithFields().Error(err)
		return err
	}

	h.chart, err = loader.Load(*h.chartPath)

	if err != nil {
		h.logWithFields().Error(err)
		return err
	}

	h.chartValues, err = NewHelmChartValues(h.settings, &h.ValueFiles)

	if err != nil {
		h.logWithFields().Error(err)
		return err
	}

	h.chart.Metadata.Description = h.appRevision.String()

	return nil
}

func (h *HelmProvider) install() error {

	install := action.NewInstall(h.cfg)

	install.ReleaseName = *h.releaseName
	install.Namespace = *h.namespace

	install.Wait = true

	install.Timeout = h.ActionOptions.actionTimeout
	install.DependencyUpdate = true
	install.CreateNamespace = h.ActionOptions.CreateNamespace
	install.IncludeCRDs = h.ActionOptions.IncludeCRDs
	install.Atomic = h.ActionOptions.Atomic
	install.Description = h.appRevision.String()

	_, err := install.Run(h.chart, h.chartValues)

	if err != nil {
		h.logWithFields().Error(err)
		return err
	}

	return nil
}

func (h *HelmProvider) upgrade() error {

	upgrade := action.NewUpgrade(h.cfg)

	upgrade.Namespace = *h.namespace
	upgrade.Install = true

	upgrade.CleanupOnFail = true

	upgrade.Timeout = h.ActionOptions.actionTimeout
	upgrade.SkipCRDs = !h.ActionOptions.IncludeCRDs
	upgrade.Wait = true
	upgrade.Atomic = h.ActionOptions.Atomic
	upgrade.DependencyUpdate = true
	upgrade.Recreate = false
	upgrade.Description = h.appRevision.String()

	if _, err := upgrade.Run(*h.releaseName, h.chart, h.chartValues); err != nil {
		h.logWithFields().Error(err)
		return err
	}

	return nil
}

func (h *HelmProvider) Uninstall() error {

	uninstall := action.NewUninstall(h.cfg)

	uninstall.Timeout = h.ActionOptions.actionTimeout
	uninstall.Wait = true
	uninstall.KeepHistory = false

	if _, err := uninstall.Run(*h.releaseName); err != nil {
		h.logWithFields().Error(err)
		return err
	}

	return nil
}

func (h *HelmProvider) getCurrentRelease() (*release.Release, error) {
	releaseList := action.NewList(h.cfg)

	releaseList.All = true
	releaseList.StateMask = action.ListAll

	allReleaseList, err := releaseList.Run()

	if err != nil {
		return nil, err
	}

	for _, r := range allReleaseList {
		if r.Name == *h.releaseName {
			return r, nil
		}
	}

	return nil, nil
}

func (h *HelmProvider) Delivery() error {
	currentRelease, err := h.getCurrentRelease()

	if err != nil {
		h.logWithFields().Error(err)
		return err
	}

	if err := h.loadHelmChartWithValues(); err != nil {
		h.logWithFields().Error(err)
		return err
	}

	if currentRelease == nil {
		if err := h.install(); err != nil {
			h.logWithFields().Error(err)
			return err
		}
	} else {

		h.chartValues, err = NewHelmChartValues(h.settings, &h.ValueFiles)

		if err != nil {
			h.logWithFields().Error(err)
			return err
		}

		// app revision hash set to Chart.Metadata.Description
		// for chart control from this application
		// maybe need to use another field for that
		if currentRelease.Chart.Metadata.Description == h.appRevision.String() {
			h.logWithFields().Info(
				"already exist, skip Delivery",
			)
			return nil
		}

		if h.ActionOptions.CheckValuesEqual {
			if reflect.DeepEqual(currentRelease.Chart.Values, h.chartValues) {
				h.logWithFields().Info(
					"values not changed, skip Delivery due checkValuesEqual option",
				)
				return nil
			}
		}

		log.Debugf("%+v", currentRelease.Info)

		h.logWithFields().Infof(
			"current %s", currentRelease.Chart.Metadata.Description,
		)

		h.logWithFields().Info(
			"wanted",
		)

		if h.ActionOptions.ReInstallRelease {
			if err := h.Uninstall(); err != nil {
				h.logWithFields().Error(err)
				return err
			}

			if err := h.install(); err != nil {
				h.logWithFields().Error(err)
				return err
			}
		} else {
			if err := h.upgrade(); err != nil {
				h.logWithFields().Error(err)
				return err
			}
		}
	}

	h.logWithFields().Infof("delivered")

	return nil
}
