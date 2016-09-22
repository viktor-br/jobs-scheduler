package scheduler

import (
	"fmt"
	"github.com/satori/go.uuid"
	"strings"
	"sync/atomic"
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

func TestScheduleJobs(t *testing.T) {
	var resultsNum int64 = 0
	scheduler := NewJobsScheduler(func(job Job) JobResult {
		jobResult := JobResultTest{}

		return jobResult
	})

	scheduler.AddResultOutput(func(res JobResult) {
		atomic.AddInt64(&resultsNum, 1)
	})

	go scheduleJobs(scheduler)
	job := JobTest{uuid.NewV4().String()}
	jobUnit := Task{ID: uuid.NewV4().String(), tries: 0, job: job}
	scheduler.jobsChannel <- jobUnit
	assertLoggerChannelMsg(
		t,
		"No message job added to a queue",
		fmt.Sprintf("scheduler: %s added to the queue", jobUnit.ID),
		scheduler.loggerChannel,
	)

	res := JobResultTest{job: job}
	jobResultUnit := TaskResult{jobUnit.ID, res}
	scheduler.resultsChannel <- jobResultUnit
	assertLoggerChannelMsg(
		t,
		"No message scheduler received result",
		fmt.Sprintf("scheduler: %s result received", jobResultUnit.taskID),
		scheduler.loggerChannel,
	)
	scheduler.resultsChannel <- jobResultUnit
	assertLoggerChannelMsg(
		t,
		"No message scheduler received result",
		fmt.Sprintf("scheduler: %s result received", jobResultUnit.taskID),
		scheduler.loggerChannel,
	)
	scheduler.resultsChannel <- jobResultUnit
	//assertLoggerChannelMsg(
	//	t,
	//	"No message scheduler received result",
	//	fmt.Sprintf("scheduler: %s result received", jobResultUnit.taskID),
	//	scheduler.loggerChannel,
	//)
	//assertLoggerChannelMsg(
	//	t,
	//	"Scheduler didn't reschedule job",
	//	fmt.Sprintf("scheduler: %s rescheduled", jobResultUnit.taskID),
	//	scheduler.loggerChannel,
	//)

	// Send terminate signal
	scheduler.jobsDone <- struct{}{}
	select {
	case <-scheduler.jobsDone:
	case <-time.After(2 * time.Second):
		t.Errorf("scheduleJobs didn't exit after 2 seconds")
	}

	if resultsNum != 3 {
		t.Errorf("Output results number id %d, expected %d", resultsNum, 3)
	}
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
	logMessagesLen := int64(0)
	scheduler := NewJobsScheduler(func(job Job) JobResult {
		jobResult := JobResultTest{}

		return jobResult
	})
	scheduler.AddLogger(func(msg string) {
		atomic.AddInt64(&logMessagesLen, 1)
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
	// TODO We run 101 goroutines without completion handling, so put sleep for 1 second.
	time.Sleep(1 * time.Second)
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
