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
	"time"

	//k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// RepositoryReconciler reconciles a Repository object
type RepositoryReconciler struct {
	client.Client
	DummyClient  *dummycd.Client
	Scheme       *runtime.Scheme
	Repositories *dummycdv1alpha1.RepositoryList
}

//+kubebuilder:rbac:groups=dummy.cd,resources=repositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=dummy.cd,resources=repositories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=dummy.cd,resources=repositories/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

func (r *RepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	repo := &dummycdv1alpha1.Repository{}

	if err := r.Get(ctx, req.NamespacedName, repo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	finalizerName := "repo.dummy.cd/finalizer"

	if repo.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(repo, finalizerName) {

			controllerutil.AddFinalizer(repo, finalizerName)

			if err := r.Update(ctx, repo); err != nil {
				log.Error(err, "failed to update the repository")
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(repo, finalizerName) {
			log.Info("start deleting the repository")

			_, err := r.DummyClient.DeleteRepository(ctx, &pb.Repository{
				Name: req.Name,
			})

			if err != nil {
				log.Error(err, "failed to delete the repository")
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(repo, finalizerName)

			if err := r.Update(ctx, repo); err != nil {
				log.Error(err, "failed to update the repository")
				return ctrl.Result{}, err
			}
			log.Info("the repository deleted")
		}

		return ctrl.Result{}, nil
	}

	log.Info("sync the repository")

	_, err := r.DummyClient.AddRepository(ctx, &pb.Repository{
		Name:                  req.Name,
		Url:                   repo.Spec.URL,
		PrivateKeySecret:      repo.Spec.PrivateKeySecret,
		InsecureIgnoreHostKey: repo.Spec.InsecureIgnoreHostKey,
	})

	if err != nil {
		log.Error(err, "failed to sync the repository")
		return ctrl.Result{}, err
	}

	log.Info("the repository synced")

	return ctrl.Result{RequeueAfter: time.Duration(1) * time.Minute}, nil
}

// SetupWithManager sets up the operator with the Manager.
func (r *RepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dummycdv1alpha1.Repository{}).
		Complete(r)
}
