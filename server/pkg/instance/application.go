package instance

import (
	"errors"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	log "github.com/sirupsen/logrus"
	"github.com/yimgzz/dummy-cd/server/pkg/provider"
	"golang.org/x/net/context"
	"helm.sh/helm/v3/pkg/chartutil"
	"k8s.io/client-go/rest"
	"os"
	"path"
	"strings"
	"sync"
)

// Application holds git repository, git options and delivery provider
type Application struct {
	Name             string `json:"name"`
	Namespace        string `json:"namespace"`
	URL              string `json:"url"`
	Reference        string `json:"reference"`
	SparsePath       string `json:"sparsePath"`
	storagePath      string
	handledPath      string
	CurrentRevision  plumbing.Hash
	cloneOptions     *git.CloneOptions
	checkoutOptions  *git.CheckoutOptions
	pullOptions      *git.PullOptions
	fetchOptions     *git.FetchOptions
	logOptions       *git.LogOptions
	RepositoryConfig *RepositoryConfig
	repo             *git.Repository
	Helm             *provider.HelmProvider
	mutex            *sync.Mutex
	deliveryProvider provider.DeliveryProvider
}

func NewApplication(ctx context.Context, app *Application, restKubeConfig *rest.Config) (*Application, error) {

	app.mutex = new(sync.Mutex)
	app.mutex.Lock()
	defer app.mutex.Unlock()

	var err error

	app.storagePath = path.Join(Workspace, app.Name)
	app.handledPath = path.Join(app.storagePath, app.SparsePath)

	referenceName := app.getLocalReferenceName()

	app.cloneOptions = app.RepositoryConfig.GetCloneOptions(&referenceName)

	app.fetchOptions = app.RepositoryConfig.GetFetchOptions()

	app.pullOptions = app.RepositoryConfig.GetPullOptions(&referenceName)

	app.checkoutOptions = &git.CheckoutOptions{
		SparseCheckoutDirectories: []string{app.SparsePath},
		Keep:                      false,
		Branch:                    referenceName,
		Force:                     true,
	}

	app.logOptions = &git.LogOptions{
		PathFilter: func(s string) bool {
			if len(app.SparsePath) == 0 {
				return true
			} else {
				return strings.HasPrefix(s, app.SparsePath)
			}
		},
	}

	app.repo, err = NewGitRepository(ctx, &app.storagePath, app.cloneOptions, app.checkoutOptions)

	if err != nil {
		return nil, err
	}

	_, err = chartutil.IsChartDir(app.handledPath)

	if err != nil {
		app.deliveryProvider, err = provider.NewRawProvider(ctx, &app.Name, &app.handledPath, &app.Namespace, restKubeConfig, &app.CurrentRevision)

		if err != nil {
			app.logWithFields().Error(err)
			return nil, err
		}

		app.logWithFields().Info("delivery as raw k8s resources")
	} else {
		app.deliveryProvider, err = provider.NewHelmProvider(&app.Name, &app.handledPath, &app.Namespace, app.Helm, restKubeConfig, &app.CurrentRevision)

		if err != nil {
			app.logWithFields().Error(err)
			return nil, err
		}

		app.logWithFields().Info("delivery as helm release")
	}

	return app, nil
}

func (a *Application) logWithFields() *log.Entry {
	return log.WithFields(log.Fields{"app": a.Name, "revision": a.CurrentRevision.String()})
}

func (a *Application) getLocalReferenceName() plumbing.ReferenceName {
	return plumbing.ReferenceName("refs/heads/" + a.Reference)
}

func (a *Application) getRemoteReferenceName() plumbing.ReferenceName {
	return plumbing.ReferenceName("refs/remotes/origin/" + a.Reference)
}

// Uninstall the application using DeliveryProvider
func (a *Application) Uninstall() error {
	err := a.deliveryProvider.Uninstall()

	if err != nil {
		a.logWithFields().Error(err)
		return err
	}

	err = os.RemoveAll(a.storagePath)

	if err != nil {
		a.logWithFields().Error(err)
		return err
	}

	return nil
}

// RunLifeCycle fetching and pulling updates, checking revision delivered
func (a *Application) RunLifeCycle() error {
	err := a.repo.Fetch(a.fetchOptions)

	if err != nil {
		if err == git.NoErrAlreadyUpToDate {
			if !a.CurrentRevision.IsZero() {
				//is app synced with k8s?
				err = a.deliveryProvider.Delivery()

				if err != nil {
					a.logWithFields().Error(err)
					return err
				}

				return nil
			}
		} else {
			a.logWithFields().Error(err)
			return err
		}
	}

	a.logWithFields().Debug("updates fetched, starting update repository")

	localReference, err := a.repo.Reference(a.getLocalReferenceName(), true)

	if err != nil {
		a.logWithFields().Error(err)
		return err
	}

	remoteReference, err := a.repo.Reference(a.getRemoteReferenceName(), true)

	if err != nil {
		a.logWithFields().Error(err)
		return err
	}

	worktree, err := a.repo.Worktree()

	if err != nil {
		a.logWithFields().Error(err)
		return err
	}

	if localReference.Hash() != remoteReference.Hash() {
		a.logWithFields().Debug("local and remote hash are different")

		//err = worktree.Reset(&git.ResetOptions{
		//	Commit: remoteReference.Hash(),
		//	Mode:   git.HardReset,
		//})

		err = worktree.ResetSparsely(&git.ResetOptions{
			Commit: remoteReference.Hash(),
			Mode:   git.HardReset,
		}, []string{a.SparsePath})

		if err != nil {
			a.logWithFields().Error(err)
			return err
		}

		a.logWithFields().Debug("git hard reset done")
	} else {
		err = worktree.Checkout(a.checkoutOptions)

		if err != nil {
			a.logWithFields().Error(err)
			return err
		}

		err = worktree.Pull(a.pullOptions)

		if !a.CurrentRevision.IsZero() &&
			(err == git.NoErrAlreadyUpToDate) {
			a.logWithFields().Debug("already up to date")
			return nil
		}

		if (err != nil) &&
			!(err == git.NoErrAlreadyUpToDate) {
			a.logWithFields().Error(err)
			return err
		}

		a.logWithFields().Debug("pulled updates")
	}

	worktree, err = a.repo.Worktree()

	if err != nil {
		a.logWithFields().Error(err)
		return err
	}

	err = worktree.Checkout(a.checkoutOptions)

	if err != nil {
		a.logWithFields().Error(err)
		return err
	}

	headMatchedRevision := a.getApplicationHeadRevision()

	if headMatchedRevision.IsZero() {
		err := errors.New("unexpected: application head matched revision hash is zero")
		a.logWithFields().Error(err)
		return err
	}

	if a.CurrentRevision == headMatchedRevision {
		a.logWithFields().Debug("already up to date")
		return nil
	}

	a.CurrentRevision = headMatchedRevision

	a.logWithFields().Debug("done update repository")

	//install after updating
	err = a.deliveryProvider.Delivery()

	if err != nil {
		a.logWithFields().Error(err)
		return err
	}

	return nil
}

func (a *Application) getRevisionsWithPathFilter() (object.CommitIter, error) {

	iter, err := a.repo.Log(&git.LogOptions{
		PathFilter: func(s string) bool {
			if len(a.SparsePath) == 0 {
				return true
			} else {
				return strings.HasPrefix(s, a.SparsePath)
			}
		},
	})

	if err != nil {
		return nil, err
	}

	return iter, nil
}

func (a *Application) getApplicationHeadRevision() plumbing.Hash {
	revisions, err := a.getRevisionHashList()

	if err != nil {
		a.logWithFields().Error(err)
		return plumbing.ZeroHash
	}

	if revisions == nil {
		a.logWithFields().Error("unexpected: revisions is nil")
		return plumbing.ZeroHash
	}

	return revisions[0]
}

func (a *Application) getRevisionHashList() ([]plumbing.Hash, error) {
	iter, err := a.getRevisionsWithPathFilter()

	if err != nil {
		a.logWithFields().Error(err)
		return nil, err
	}

	var list []plumbing.Hash
	_ = iter.ForEach(func(commit *object.Commit) error {
		list = append(list, commit.Hash)

		return nil
	})

	return list, nil
}

func (a *Application) GetRevisionStringsMap() ([]map[string]string, error) {
	iter, err := a.getRevisionsWithPathFilter()

	if err != nil {
		return nil, err
	}

	var revisions []map[string]string

	err = iter.ForEach(func(c *object.Commit) error {
		revisions = append(revisions, map[string]string{
			"hash": c.Hash.String(), "message": c.Message,
		})
		return nil
	})

	return revisions, nil
}

//func (a *Application) checkoutRevision(revision plumbing.Hash) error {
//
//	checkoutOptions := &git.CheckoutOptions{
//		SparseCheckoutDirectories: []string{a.SparsePath},
//		Keep:                      false,
//		Hash:                      revision,
//		Force:                     true,
//	}
//
//	if err := a.Checkout(checkoutOptions); err != nil {
//		a.logWithFields().Error(err)
//		return err
//	}
//
//	if err := a.SetRevision(checkoutOptions); err != nil {
//		a.logWithFields().Error(err)
//		return err
//	}
//
//	if err := a.helm.Delivery(); err != nil {
//		a.logWithFields().Error(err)
//		return err
//	}
//
//	a.logWithFields().Infof("%s applied", revision.String())
//
//	return nil
//}
