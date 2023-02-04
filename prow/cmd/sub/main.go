/*
Copyright 2018 The Kubernetes Authors.

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

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/pkg/flagutil"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowv1 "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/crier/reporters/pubsub"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil/pprof"
	"k8s.io/test-infra/prow/pubsub/subscriber"
)

type options struct {
	client         prowflagutil.KubernetesOptions
	github         prowflagutil.GitHubOptions
	port           int
	cookiefilePath string

	config configflagutil.ConfigOptions

	dryRun                 bool
	gracePeriod            time.Duration
	instrumentationOptions prowflagutil.InstrumentationOptions
}

func (o *options) validate() error {
	var errs []error
	for _, group := range []flagutil.OptionGroup{&o.client, &o.github, &o.instrumentationOptions, &o.config} {
		if err := group.Validate(o.dryRun); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options
	fs.IntVar(&o.port, "port", 80, "HTTP Port.")
	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	fs.DurationVar(&o.gracePeriod, "grace-period", 180*time.Second, "On shutdown, try to handle remaining events for the specified duration. ")
	fs.StringVar(&o.cookiefilePath, "cookiefile", "", "Path to git http.cookiefile, leave empty for github or anonymous")
	for _, group := range []flagutil.OptionGroup{&o.client, &o.github, &o.instrumentationOptions, &o.config} {
		group.AddFlags(fs)
	}

	fs.Parse(args)

	return o
}

type kubeClient struct {
	client prowv1.ProwJobInterface
	dryRun bool
}

func (c *kubeClient) Create(ctx context.Context, job *prowapi.ProwJob, o metav1.CreateOptions) (*prowapi.ProwJob, error) {
	if c.dryRun {
		return job, nil
	}
	return c.client.Create(ctx, job, o)
}

// Get is not implemented for sub's use of ProwJobClient because Sub does not
// use it (from the clients who use Sub, it is a "fire and forget" type of
// interaction). However we still use a stub here because Gangway internals
// expect an interface that can support both Create() and Get().
func (c *kubeClient) Get(ctx context.Context, jobId string, o metav1.GetOptions) (*prowapi.ProwJob, error) {
	return nil, fmt.Errorf("unimplemented")
}

func main() {
	logrusutil.ComponentInit()

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}

	prowjobClient, err := o.client.ProwJobClient(configAgent.Config().ProwJobNamespace, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("unable to create prow job client")
	}
	kubeClient := &kubeClient{
		client: prowjobClient,
		dryRun: o.dryRun,
	}

	promMetrics := subscriber.NewMetrics()

	defer interrupts.WaitForGracefulShutdown()

	// Expose prometheus and pprof metrics
	metrics.ExposeMetrics("sub", configAgent.Config().PushGateway, o.instrumentationOptions.MetricsPort)
	pprof.Instrument(o.instrumentationOptions)

	// If we are provided credentials for Git hosts, use them. These credentials
	// hold per-host information in them so it's safe to set them globally.
	if o.cookiefilePath != "" {
		cmd := exec.Command("git", "config", "--global", "http.cookiefile", o.cookiefilePath)
		if err := cmd.Run(); err != nil {
			logrus.WithError(err).Fatal("unable to set cookiefile")
		}
	}

	gitClient, err := o.github.GitClientFactory(o.cookiefilePath, &o.config.InRepoConfigCacheDirBase, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting Git client.")
	}
	cacheGetter, err := config.NewInRepoConfigCacheHandler(o.config.InRepoConfigCacheSize, configAgent, gitClient, o.config.InRepoConfigCacheCopies)
	if err != nil {
		logrus.WithError(err).Fatal("Error creating InRepoConfigCacheGetter.")
	}

	s := &subscriber.Subscriber{
		ConfigAgent:              configAgent,
		Metrics:                  promMetrics,
		ProwJobClient:            kubeClient,
		Reporter:                 pubsub.NewReporter(configAgent.Config), // reuse crier reporter
		InRepoConfigCacheHandler: cacheGetter,
	}

	subMux := http.NewServeMux()
	// Return 200 on / for health checks.
	subMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})

	// Setting up Pull Server
	logrus.Info("Setting up Pull Server")
	pullServer := subscriber.NewPullServer(s)
	interrupts.Run(func(ctx context.Context) {
		if err := pullServer.Run(ctx); err != nil {
			logrus.WithError(err).Fatal("Failed to run Pull Server")
		}
	})

	httpServer := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: subMux}
	interrupts.ListenAndServe(httpServer, o.gracePeriod)
}
