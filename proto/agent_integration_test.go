package proto

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/evergreen-ci/evergreen"
	dbutil "github.com/evergreen-ci/evergreen/db"
	"github.com/evergreen-ci/evergreen/model/host"
	modelutil "github.com/evergreen-ci/evergreen/model/testutil"
	"github.com/evergreen-ci/evergreen/rest/client"
	"github.com/evergreen-ci/evergreen/service"
	"github.com/evergreen-ci/evergreen/testutil"
	"github.com/stretchr/testify/suite"
)

type testConfigPath struct {
	testSpec string
}

func createAgent(testServer *service.TestServer, testHost *host.Host) *Agent {
	initialOptions := Options{
		APIURL:            testServer.URL,
		HostID:            testHost.Id,
		HostSecret:        testHost.Secret,
		StatusPort:        2285,
		LogPrefix:         "log_prefix",
		HeartbeatInterval: 5 * time.Second,
	}
	return New(initialOptions, client.NewCommunicator(testServer.URL))
}

type AgentIntegrationSuite struct {
	suite.Suite
	a             *Agent
	tc            *taskContext
	testDirectory string
	modelData     *modelutil.TestModelData
	testConfig    *evergreen.Settings
	testServer    *service.TestServer
}

func TestAgentIntegrationSuite(t *testing.T) {
	suite.Run(t, new(AgentIntegrationSuite))
}

func (s *AgentIntegrationSuite) SetupSuite() {
	s.testConfig = testutil.TestConfig()
	testutil.ConfigureIntegrationTest(s.T(), s.testConfig, "AgentIntegrationSuite")
	dbutil.SetGlobalSessionProvider(dbutil.SessionFactoryFromConfig(s.testConfig))
}

func (s *AgentIntegrationSuite) TearDownTest() {
	if s.testServer != nil {
		s.testServer.Close()
	}
}

func (s *AgentIntegrationSuite) TestRunTask() {
	var err error
	s.testDirectory = testutil.GetDirectoryOfFile()
	s.modelData, err = modelutil.SetupAPITestData(s.testConfig, "print_dir_task", "linux-64", filepath.Join(s.testDirectory, "testdata/agent-integration.yml"), modelutil.NoPatch)
	if err != nil {
		panic(err)
	}
	testServer, err := service.CreateTestServer(s.testConfig, nil)
	if err != nil {
		panic(err)
	}
	s.testServer = testServer
	s.a = createAgent(testServer, s.modelData.Host)
	s.tc = &taskContext{
		task: client.TaskData{
			ID:     s.modelData.Task.Id,
			Secret: s.modelData.Task.Secret,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = s.a.startNextTask(ctx, s.tc)
	s.NoError(err)
}

func (s *AgentIntegrationSuite) TestAbortTask() {
	var err error
	s.testDirectory = testutil.GetDirectoryOfFile()
	s.modelData, err = modelutil.SetupAPITestData(s.testConfig, "very_slow_task", "linux-64", filepath.Join(s.testDirectory, "testdata/agent-integration.yml"), modelutil.NoPatch)
	if err != nil {
		panic(err)
	}
	testServer, err := service.CreateTestServer(s.testConfig, nil)
	if err != nil {
		panic(err)
	}
	s.testServer = testServer
	s.a = createAgent(testServer, s.modelData.Host)
	s.tc = &taskContext{
		task: client.TaskData{
			ID:     s.modelData.Task.Id,
			Secret: s.modelData.Task.Secret,
		},
	}

	errChan := make(chan error)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		err = s.a.startNextTask(ctx, s.tc)
		errChan <- err
	}()
	cancel()
	err = <-errChan
	s.Error(err)
}
