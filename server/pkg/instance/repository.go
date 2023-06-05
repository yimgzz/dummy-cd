package instance

import (
	"context"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	log "github.com/sirupsen/logrus"
	"github.com/yimgzz/dummy-cd/server/pkg/provider"
	"github.com/yimgzz/dummy-cd/server/pkg/util"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
)

// RepositorySettings holds repository from dummy.cd/v1alpha1.Repository CR
type RepositorySettings struct {
	URL                   string
	PrivateKeySecret      string
	InsecureIgnoreHostKey bool
}

// NewSSHRepositoryAuth returns PublicKeys AuthMethod for git repository
func NewSSHRepositoryAuth(settings *RepositorySettings, restKubeConfig *rest.Config, currentNamespace *string) (*gitssh.PublicKeys, error) {
	var auth *gitssh.PublicKeys

	clientset, err := kubernetes.NewForConfig(restKubeConfig)

	if err != nil {
		log.WithField("repo", settings.URL).Error(err)
		return nil, err
	}

	secret, err := clientset.CoreV1().Secrets(*currentNamespace).
		Get(context.TODO(), settings.PrivateKeySecret, metav1.GetOptions{})

	if err != nil {
		log.WithField("repo", settings.URL).Error(err)
		return nil, err
	}

	auth, err = gitssh.NewPublicKeys("git", secret.Data["sshPrivateKey"], "")

	if err != nil {
		log.WithField("repo", settings.URL).Error(err)
		return nil, err
	}

	var hostKeyCallback ssh.HostKeyCallback

	if settings.InsecureIgnoreHostKey {
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	} else {
		//hostKeyCallback, err = knownhosts.New(*knownHostsFile)
		var err error
		hostKeyCallback, err = knownhosts.New(filepath.Join(*util.GetUserHome(), ".ssh", "knownhosts"))

		if err != nil {
			log.WithField("repo", settings.URL).Error(err)
			return nil, err
		}
	}

	auth.HostKeyCallbackHelper = gitssh.HostKeyCallbackHelper{
		HostKeyCallback: hostKeyCallback,
	}

	return auth, nil
}

// RepositoryConfig holds repository settings and slice of Application
type RepositoryConfig struct {
	Name     string
	Settings *RepositorySettings
	auth     *gitssh.PublicKeys
	Mutex    *sync.Mutex
	Apps     []*Application
}

// NewRepositoryConfig returns new repository config
func NewRepositoryConfig(name string, settings *RepositorySettings, restKubeConfig *rest.Config, currentNamespace *string) (*RepositoryConfig, error) {

	cfg := &RepositoryConfig{
		Name:     name,
		Settings: settings,
		Mutex:    new(sync.Mutex),
	}

	cfg.Mutex.Lock()
	defer cfg.Mutex.Unlock()

	//check if url ssh
	repoURL, err := url.Parse(settings.URL)

	if (err != nil) || (repoURL.Scheme == "ssh") {
		cfg.auth, err = NewSSHRepositoryAuth(settings, restKubeConfig, currentNamespace)

		if err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// NewGitRepository returns cloned or opened git repository
func NewGitRepository(ctx context.Context, path *string,
	cloneOptions *git.CloneOptions, checkoutOptions *git.CheckoutOptions) (*git.Repository, error) {

	repo, err := git.PlainCloneContext(ctx, *path, false, cloneOptions)

	if err == git.ErrRepositoryAlreadyExists {
		repo, err = git.PlainOpen(*path)

		if err != nil {
			return nil, err
		}
	}

	if (err != nil) &&
		(err != git.ErrRepositoryAlreadyExists) {
		return nil, err
	}

	worktree, err := repo.Worktree()

	if err != nil {
		return nil, err
	}

	err = worktree.Checkout(checkoutOptions)

	if err != nil {
		return nil, err
	}

	return repo, nil
}

// GetCloneOptions returns git clone options for specific branch, path
func (r *RepositoryConfig) GetCloneOptions(referenceName *plumbing.ReferenceName) *git.CloneOptions {
	cloneOptions := &git.CloneOptions{
		URL:           r.Settings.URL,
		ReferenceName: *referenceName,
		SingleBranch:  true,
		NoCheckout:    false,
	}

	if r.auth != nil {
		cloneOptions.Auth = r.auth
	}

	return cloneOptions
}

// GetFetchOptions returns get fetch options
func (r *RepositoryConfig) GetFetchOptions() *git.FetchOptions {
	fetchOptions := &git.FetchOptions{
		RemoteName: "origin",
		RemoteURL:  r.Settings.URL,
		Force:      true,
	}

	if r.auth != nil {
		fetchOptions.Auth = r.auth
	}

	return fetchOptions
}

// GetPullOptions returns git pull options for specific branch, path
func (r *RepositoryConfig) GetPullOptions(referenceName *plumbing.ReferenceName) *git.PullOptions {
	pullOptions := &git.PullOptions{
		RemoteName:    "origin",
		RemoteURL:     r.Settings.URL,
		ReferenceName: *referenceName,
		SingleBranch:  true,
		Force:         true,
	}

	if r.auth != nil {
		pullOptions.Auth = r.auth
	}

	return pullOptions
}

// AddOrUpdateApplication adding the application if not exist, else comparing the applications with cmp.Equal() and update, if they differ
func (r *RepositoryConfig) AddOrUpdateApplication(ctx context.Context, restKubeConfig *rest.Config, application *Application) error {
	appIndex := -1

	for i, app := range r.Apps {
		if app.Name == application.Name {
			appIndex = i
			break
		}
	}

	if appIndex == -1 {
		newApplication, err := NewApplication(ctx, application, restKubeConfig)

		if err != nil {
			log.Errorf("%s: %+v", err, r)
			return err
		}

		for !r.Mutex.TryLock() {
		}
		defer r.Mutex.Unlock()

		r.Apps = append(r.Apps, newApplication)

		return nil
	}

	if cmp.Equal(r.Apps[appIndex], application,
		cmpopts.IgnoreUnexported(Application{}), cmpopts.IgnoreTypes(RepositoryConfig{}, plumbing.Hash{}, provider.HelmProvider{})) {
		return util.NoErrAlreadyUpTodate
	}

	for !r.Mutex.TryLock() {
	}
	defer r.Mutex.Unlock()

	for !r.Apps[appIndex].mutex.TryLock() {
	}
	defer r.Apps[appIndex].mutex.Unlock()

	if err := os.RemoveAll(r.Apps[appIndex].storagePath); err != nil {
		log.Errorf("%s: %+v", err, r)
		return err
	}

	var err error

	r.Apps[appIndex], err = NewApplication(ctx, application, restKubeConfig)

	if err != nil {
		log.Errorf("%s: %+v", err, r)
	}

	return err
}

// DeleteApplication deleting the application if it exists
func (r *RepositoryConfig) DeleteApplication(name string) error {
	appIndex := -1
	var application *Application

	for i, app := range r.Apps {
		if app.Name == name {
			application = app
			appIndex = i
		}
	}

	if appIndex == -1 {
		return nil
	}

	for !r.Mutex.TryLock() {
	}

	defer r.Mutex.TryLock()

	for !application.mutex.TryLock() {
	}

	defer application.mutex.TryLock()

	if err := application.deliveryProvider.Uninstall(); err != nil {
		application.logWithFields().Error(err)
	}

	r.Apps[appIndex] = r.Apps[len(r.Apps)-1]
	r.Apps = r.Apps[:len(r.Apps)-1]

	return nil
}
