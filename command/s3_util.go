package command

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/evergreen-ci/evergreen/util"
	"github.com/goamz/goamz/s3"
)

const (
	maxs3putAttempts = 5
	s3PutSleep       = 5 * time.Second
	s3baseURL        = "https://s3.amazonaws.com/"
)

var (
	// Regular expression for validating S3 bucket names
	bucketNameRegex = regexp.MustCompile(`^[A-Za-z0-9_\-.]+$`)
)

func validateS3BucketName(bucket string) error {
	// if it's an expandable string, we can't expand yet since we don't have
	// access to the task config expansions. So, we defer till during runtime
	// to do the validation
	if util.IsExpandable(bucket) {
		return nil
	}
	if len(bucket) < 3 {
		return errors.New("must be at least 3 characters")
	}
	if len(bucket) > 63 {
		return errors.New("must be no more than 63 characters")
	}
	if strings.HasPrefix(bucket, ".") || strings.HasPrefix(bucket, "-") {
		return errors.New("must not begin with a period or hyphen")
	}
	if strings.HasSuffix(bucket, ".") || strings.HasSuffix(bucket, "-") {
		return errors.New("must not end with a period or hyphen")
	}
	if strings.Contains(bucket, "..") {
		return errors.New("must not have two consecutive periods")
	}
	if !bucketNameRegex.MatchString(bucket) {
		return errors.New("must contain only combinations of uppercase/lowercase " +
			"letters, numbers, hyphens, underscores and periods")
	}
	return nil
}

func validS3Permissions(perm string) bool {
	return util.SliceContains(
		[]s3.ACL{
			s3.Private,
			s3.PublicRead,
			s3.PublicReadWrite,
			s3.AuthenticatedRead,
			s3.BucketOwnerRead,
			s3.BucketOwnerFull,
		},
		s3.ACL(perm),
	)
}
