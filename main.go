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

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/google/go-github/v31/github"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
)

type refreshJob struct {
	repo string
	dest string
}

func cloneOrFetch(job refreshJob) error {
	_, err := os.Stat(job.dest)
	if err != nil {
		if !os.IsNotExist(err) {
			return errors.WithStack(err)
		}

		_, err = git.PlainClone(job.dest, true, &git.CloneOptions{
			URL: job.repo,
		})
		return errors.WithStack(err)
	}

	repo, err := git.PlainOpen(job.dest)
	if err != nil {
		return errors.WithStack(err)
	}
	err = repo.Fetch(&git.FetchOptions{
		RefSpecs: []config.RefSpec{
			"+refs/heads/master:refs/heads/master",
			"+refs/heads/*:refs/remotes/origin/*",
		},
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return errors.WithStack(err)
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
		return errors.New("-token must be provided")
	}
	if *dest == "" {
		return errors.New("-dest must be provided")
	}

	jobs := make(chan refreshJob, *parallel*10)
	var wg sync.WaitGroup
	wg.Add(*parallel)
	for i := 0; i < *parallel; i++ {
		go refresh(&wg, jobs)
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{
			AccessToken: *token,
		},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	page := 0
	for {
		opts := github.RepositoryListOptions{
			Affiliation: "owner",
			ListOptions: github.ListOptions{
				Page: page,
			},
		}
		repos, resp, err := client.Repositories.List(ctx, "", &opts)
		if err != nil {
			return errors.WithStack(err)
		}
		for _, repo := range repos {
			if *filter != "" && !strings.Contains(*repo.Name, *filter) {
				continue
			}
			jobs <- refreshJob{
				repo: *repo.SSHURL,
				dest: filepath.Join(*dest, *repo.Name),
			}
		}
		if resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
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
