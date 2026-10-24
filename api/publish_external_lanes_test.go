package main

import (
	"testing"
	"time"
)

func testLane(accountID, name, state string) publishExternalLane {
	return publishExternalLane{
		AccountID: accountID, Name: name, State: state,
		RequestedBy: publishExternalAutoActor, RequestedAt: "2026-10-10T01:00:00Z",
	}
}

func setTestLanes(t *testing.T, env *publishTestEnv, jobID int64, lanes ...publishExternalLane) publishJob {
	t.Helper()
	job, err := env.store.update(jobID, func(current *publishJob) {
		current.ExternalDistributionLanes = append([]publishExternalLane(nil), lanes...)
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func mustLane(t *testing.T, job publishJob, accountID string) publishExternalLane {
	t.Helper()
	lane, ok := externalLaneFor(job, accountID)
	if !ok {
		t.Fatalf("任务 #%d 缺少账号 %s 的 lane：%+v", job.ID, accountID, job.ExternalDistributionLanes)
	}
	return lane
}

func mustParseRFC3339(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
