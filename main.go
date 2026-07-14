// Command ghbackup is a super simple github repository backup script.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/daaku/serr"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-github/v89/github"
)

type refreshJob struct {
	repo string
	dest string
}

func cloneOrFetch(job refreshJob) error {
	_, err := os.Stat(job.dest)
	if err != nil {
		if !os.IsNotExist(err) {
			return serr.Wrap(err)
		}

		_, err = git.PlainClone(job.dest, true, &git.CloneOptions{
			URL: job.repo,
		})
		return serr.Wrap(err)
	}

	repo, err := git.PlainOpen(job.dest)
	if err != nil {
		return serr.Wrap(err)
	}
	branches, err := repo.Branches()
	if err != nil {
		return serr.Wrap(err)
	}
	defer branches.Close()

	fo := git.FetchOptions{
		RefSpecs: []config.RefSpec{
			"+refs/heads/*:refs/remotes/origin/*",
		},
	}
	branches.ForEach(func(r *plumbing.Reference) error {
		name := r.Name().String()
		fo.RefSpecs = append(fo.RefSpecs, config.RefSpec(fmt.Sprintf("+%s:%s", name, name)))
		return nil
	})
	err = repo.Fetch(&fo)
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return serr.Wrap(err)
	}
	return nil
}

func refresh(wg *sync.WaitGroup, jobs chan refreshJob) {
	defer wg.Done()
	for job := range jobs {
		if err := cloneOrFetch(job); err != nil {
			fmt.Fprintf(os.Stderr, "for repo %q: %+v\n", job.repo, err)
		}
	}
}

func run() error {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	token := fs.String("token", "", "github token")
	parallel := fs.Int("parallel", 5, "number of repositories to clone in parallel")
	dest := fs.String("dest", "", "destination directory")
	filter := fs.String("filter", "", "filter by repository name")
	_ = fs.Parse(os.Args[1:])

	if *token == "" {
		return serr.Errorf("-token must be provided")
	}
	if *dest == "" {
		return serr.Errorf("-dest must be provided")
	}

	jobs := make(chan refreshJob, *parallel*10)
	var wg sync.WaitGroup
	wg.Add(*parallel)
	for i := 0; i < *parallel; i++ {
		go refresh(&wg, jobs)
	}

	ctx := context.Background()
	client, err := github.NewClient(github.WithAuthToken(*token))
	if err != nil {
		return serr.Wrap(err)
	}

	iter := client.Repositories.ListByAuthenticatedUserIter(ctx, &github.RepositoryListByAuthenticatedUserOptions{
		Affiliation: "owner",
	})
	for repo, err := range iter {
		if err != nil {
			return serr.Wrap(err)
		}
		if *filter != "" && !strings.Contains(*repo.Name, *filter) {
			continue
		}
		jobs <- refreshJob{
			repo: *repo.SSHURL,
			dest: filepath.Join(*dest, *repo.Name),
		}
	}
	close(jobs)
	wg.Wait()

	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%+v\n", err)
		os.Exit(1)
	}
}
