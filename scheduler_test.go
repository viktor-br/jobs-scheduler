package scheduler

import (
	"fmt"
	"github.com/satori/go.uuid"
	"strings"
	"sync"
	"testing"
	"time"
)

// Job test job, which should implements required method GetID() string
type JobTest struct {
	ID string
}

// JobResult test job result, which should implements required methods
// IsDone() and IsCorrupted(). The last returns true if processing failed
// and no need to restart the job.
type JobResultTest struct {
	lastError error
	job       Job
}

// GetID implement Job interface
func (job JobTest) GetID() string {
	return job.ID
}

// GetJobID implements required JobResult interface to use with jobs-scheduler
func (jobResult JobResultTest) GetJobID() string {
	return jobResult.job.GetID()
}

// IsDone implement JobResult interface
func (jobResult JobResultTest) IsDone() bool {
	return jobResult.lastError == nil
}

// IsCorrupted implement JobResult interface
func (jobResult JobResultTest) IsCorrupted() bool {
	return false
}

func TestProcessJob(t *testing.T) {
	scheduler := NewJobsScheduler(func(job Job) JobResult {
		jobResult := JobResultTest{}

		return jobResult
	})

	go processJob(scheduler, 0)

	job := JobTest{}
	task := Task{ID: uuid.NewV4().String(), tries: 0, job: job}
	scheduler.processorsChannel <- task
	assertLoggerChannelMsg(
		t,
		"No message about the processor received task",
		fmt.Sprintf("processor #%d: %s started", 0, task.ID),
		scheduler.loggerChannel,
	)

	assertLoggerChannelMsg(
		t,
		"No message about the processor completed task",
		fmt.Sprintf("processor #%d: %s completed", 0, task.ID),
		scheduler.loggerChannel,
	)
	assertLoggerChannelMsg(
		t,
		"No msg about pushing the result",
		fmt.Sprintf("processor #%d: %s result pushed successfully", 0, task.ID),
		scheduler.loggerChannel,
	)

	// Test closing processor
	scheduler.processorsDone <- struct{}{}
	assertLoggerChannelMsg(
		t,
		"No message about the processor received exit command",
		"processor #0: exit signal received",
		scheduler.loggerChannel,
	)

	// Check processJob called defer
	assertLoggerChannelMsg(
		t,
		"No message about the processor closed",
		"processor #0: exit",
		scheduler.loggerChannel,
	)
}

func TestProcessLogMsg(t *testing.T) {
	logMessages := []string{}
	mu := sync.Mutex{}
	scheduler := NewJobsScheduler(func(job Job) JobResult {
		jobResult := JobResultTest{}

		return jobResult
	})
	scheduler.AddLogger(func(msg string) {
		mu.Lock()
		logMessages = append(logMessages, msg)
		mu.Unlock()
	})

	go processLogMsg(scheduler)
	for i := 1; i <= 100; i++ {
		scheduler.loggerChannel <- fmt.Sprintf("Msg %d", i)
	}
	scheduler.loggerDone <- struct{}{}
	// Prevent tests lock due to implementation bug.
	select {
	case <-scheduler.loggerChannel:
	case <-time.After(2 * time.Second):
		t.Errorf("processLogMsg didn't exit after 2 seconds")

	}
	// TODO We run 101 goroutines without any completion notification, so put sleep for 1 second.
	time.Sleep(1 * time.Second)
	logMessagesLen := len(logMessages)
	if logMessagesLen != 101 {
		t.Errorf("Expects the logger processed 101 elements, but %d given", logMessagesLen)
	}
}

func assertLoggerChannelMsg(t *testing.T, errorMsg string, expectedMsg string, channel chan string) {
	givenMsg := <-channel
	if strings.Compare(givenMsg, expectedMsg) != 0 {
		t.Errorf(errorMsg)
	}
}
