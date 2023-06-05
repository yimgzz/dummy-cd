/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	dummycdv1alpha1 "github.com/yimgzz/dummy-cd/operator/api/v1alpha1"
	"github.com/yimgzz/dummy-cd/server/pkg/pb"
	dummycd "github.com/yimgzz/dummy-cd/server/pkg/server"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"time"
)

// ApplicationReconciler reconciles a Application object
type ApplicationReconciler struct {
	client.Client
	DummyClient *dummycd.Client
	Scheme      *runtime.Scheme
}

//+kubebuilder:rbac:groups=dummy.cd,resources=applications,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=dummy.cd,resources=applications/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=dummy.cd,resources=applications/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

func (r *ApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	app := &dummycdv1alpha1.Application{}

	if err := r.Get(ctx, req.NamespacedName, app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	finalizerName := "app.dummy.cd/finalizer"

	if app.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(app, finalizerName) {

			controllerutil.AddFinalizer(app, finalizerName)

			if err := r.Update(ctx, app); err != nil {
				log.Error(err, "failed to update the application")
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(app, finalizerName) {
			log.Info("start deleting the application")

			_, err := r.DummyClient.DeleteApplication(ctx, &pb.Application{
				Name: req.Name,
				Url:  app.Spec.URL,
			})

			if err != nil {
				log.Error(err, "failed to delete the application")
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(app, finalizerName)

			if err := r.Update(ctx, app); err != nil {
				log.Error(err, "failed to update the application")
				return ctrl.Result{}, err
			}
			log.Info("the application deleted")
		}

		return ctrl.Result{}, nil
	}

	log.Info("sync the application")

	_, err := r.DummyClient.AddOrUpdateApplication(ctx, &pb.Application{
		Url:        app.Spec.URL,
		Name:       req.Name,
		Namespace:  app.Spec.Namespace,
		Reference:  app.Spec.Reference,
		SparsePath: app.Spec.SparsePath,
		Helm: &pb.HelmProvider{
			CheckValuesEqual: app.Spec.Helm.CheckValuesEqual,
			ReInstallRelease: app.Spec.Helm.ReInstallRelease,
			CreateNamespace:  app.Spec.Helm.CreateNamespace,
			Atomic:           app.Spec.Helm.Atomic,
			IncludeCRDs:      app.Spec.Helm.IncludeCRDs,
			ValuesFiles:      app.Spec.Helm.ValuesFiles,
		},
	})

	if err != nil {
		log.Error(err, "error wile sync the application")

		return ctrl.Result{}, err
	}

	log.Info("the application synced")

	return ctrl.Result{RequeueAfter: time.Duration(1) * time.Minute}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dummycdv1alpha1.Application{}).
		Complete(r)
}
