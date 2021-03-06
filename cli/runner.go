package cli

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	// import the plugins here so that they're loaded for use in
	// the repotracker which needs them to do command validation.
	_ "github.com/evergreen-ci/evergreen/plugin/config"

	"github.com/evergreen-ci/evergreen"
	"github.com/evergreen-ci/evergreen/alerts"
	"github.com/evergreen-ci/evergreen/db"
	"github.com/evergreen-ci/evergreen/hostinit"
	"github.com/evergreen-ci/evergreen/monitor"
	"github.com/evergreen-ci/evergreen/notify"
	"github.com/evergreen-ci/evergreen/repotracker"
	"github.com/evergreen-ci/evergreen/scheduler"
	"github.com/evergreen-ci/evergreen/taskrunner"
	"github.com/evergreen-ci/evergreen/util"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/level"
	"github.com/mongodb/grip/message"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

const (
	schedulerInterval  = 20 * time.Second
	hostinitInterval   = 20 * time.Second
	defaultRunInterval = 60 * time.Second
)

type ServiceRunnerCommand struct {
	ConfigPath string `long:"conf" default:"/etc/mci_settings.yml" description:"path to the service configuration file"`
	Single     string `long:"single" default:"" description:"specify "`
}

func (c *ServiceRunnerCommand) Execute(_ []string) error {
	settings, err := evergreen.NewSettings(c.ConfigPath)
	if err != nil {
		return errors.Wrap(err, "problem getting settings")
	}

	sender, err := settings.GetSender(settings.Runner.LogFile)
	grip.CatchEmergencyFatal(err)
	defer sender.Close()
	grip.CatchEmergencyFatal(grip.SetSender(sender))
	evergreen.SetLegacyLogger()
	grip.SetName("evg-runner")
	grip.SetDefaultLevel(level.Info)
	grip.SetThreshold(level.Debug)

	grip.Notice(message.Fields{"build": evergreen.BuildRevision, "process": grip.Name()})

	if home := evergreen.FindEvergreenHome(); home == "" {
		grip.EmergencyFatal("EVGHOME environment variable must be set to execute runner")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go evergreen.SystemInfoCollector(ctx)
	defer util.RecoverAndLogStackTrace()
	db.SetGlobalSessionProvider(db.SessionFactoryFromConfig(settings))

	// just run a single runner if only one was passed in.
	if c.Single != "" {
		return runProcessByName(c.Single, settings)
	}

	// start and schedule runners
	//
	go listenForSIGTERM(cancel)
	startRunners(ctx, settings)

	return nil
}

////////////////////////////////////////////////////////////////////////
//
// Running and executing the offline operations processing.

type processRunner interface {
	// Name returns the id of the process runner.
	Name() string

	// Run executes the process runner with the supplied configuration.
	Run(*evergreen.Settings) error
}

var backgroundRunners = []processRunner{
	&hostinit.Runner{},
	&monitor.Runner{},
	&notify.Runner{},
	&repotracker.Runner{},
	&taskrunner.Runner{},
	&alerts.QueueProcessor{},
	&scheduler.Runner{},
}

// startRunners starts a goroutine for each runner exposed via Runners. It
// returns a channel on which all runners listen on, for when to terminate.
func startRunners(ctx context.Context, s *evergreen.Settings) {
	wg := &sync.WaitGroup{}

	duration := defaultRunInterval
	if s.Runner.IntervalSeconds > 0 {
		duration = time.Duration(s.Runner.IntervalSeconds) * time.Second
	}

	grip.Noticef("runner periodicity set to %s (default: %s, scheduler: %s)",
		time.Duration(s.Runner.IntervalSeconds)*time.Second,
		defaultRunInterval, schedulerInterval)

	for _, r := range backgroundRunners {
		wg.Add(1)

		if r.Name() == scheduler.RunnerName {
			go runnerBackgroundWorker(ctx, r, s, schedulerInterval, wg)
		} else if r.Name() == hostinit.RunnerName {
			go runnerBackgroundWorker(ctx, r, s, hostinitInterval, wg)
		} else {
			go runnerBackgroundWorker(ctx, r, s, duration, wg)
		}
	}

	wg.Wait()
	grip.Infof("Cleanly terminated all %d processes", len(backgroundRunners))
}

// listenForSIGTERM listens for the SIGTERM signal and closes the
// channel on which each runner is listening as soon as the signal
// is received.
func listenForSIGTERM(cancel context.CancelFunc) {
	sigChan := make(chan os.Signal)
	// notify us when SIGTERM is received
	signal.Notify(sigChan, syscall.SIGTERM)
	<-sigChan
	grip.Infof("Terminating %d processes", len(backgroundRunners))
	cancel()
}

// runProcessByName runs a single process given its name and evergreen Settings.
// Returns an error if the process does not exist.
func runProcessByName(name string, settings *evergreen.Settings) error {
	for _, r := range backgroundRunners {
		if r.Name() == name {
			grip.Infof("Running standalone %s process", name)
			if err := r.Run(settings); err != nil {
				grip.Error(err)
			}
			return nil
		}
	}
	return errors.Errorf("process '%s' does not exist", name)
}

func runnerBackgroundWorker(ctx context.Context, r processRunner, s *evergreen.Settings, dur time.Duration, wg *sync.WaitGroup) {
	timer := time.NewTimer(0)
	defer wg.Done()
	defer timer.Stop()

	grip.Infoln("Starting runner process:", r.Name())

	for {
		select {
		case <-ctx.Done():
			grip.Infoln("Cleanly terminated runner process:", r.Name())
			return
		case <-timer.C:
			if err := r.Run(s); err != nil {
				subject := fmt.Sprintf("%s failure", r.Name())
				grip.Error(err)
				if err = notify.NotifyAdmins(subject, err.Error(), s); err != nil {
					grip.Errorf("sending email: %+v", err)
				}
			}

			grip.Debugln("restarting runner loop for:", r.Name())
			timer.Reset(dur)
		}
	}
}
