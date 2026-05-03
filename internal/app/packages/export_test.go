package packages

// This file is only compiled during tests.
// It exposes private maps so external test package can populate them
// without having to go through Track/Check/WatchCheck goroutines.

import (
	"fmt"
	"time"
)

// SetOperationResult populates operationResults map for given user+package pair.
// Used by tests for GetTrackStatus and GetCheckStatus.
func SetOperationResult(userID, packageID int64, failed, watchable bool, errMsg, name, branch string) {
	operationResults.Store(fmt.Sprintf("%d:%d", userID, packageID), operationResult{
		failed:    failed,
		watchable: watchable,
		errMsg:    errMsg,
		name:      name,
		branch:    branch,
		createdAt: time.Now(),
	})
}
