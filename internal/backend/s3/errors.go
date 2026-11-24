package s3

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

type awsError struct{ code, hint string }

func (e *awsError) Error() string { return fmt.Sprintf("AWS %s: %s", e.code, e.hint) }

var awsCodePattern = regexp.MustCompile(`An error occurred \(([A-Za-z0-9]+)\) when calling the [A-Za-z0-9]+ operation`)

// requestError retains only recognized error codes and our own hints. Neither
// arbitrary provider messages nor stdout may reach logs or user-facing errors.
func requestError(stderr string) error {
	match := awsCodePattern.FindStringSubmatch(stderr)
	if len(match) != 2 {
		return errors.New("AWS CLI request failed; check AWS credentials, region and bucket permissions")
	}
	code := match[1]
	if code == "404" || code == "NoSuchKey" || code == "NoSuchBucket" {
		return fmt.Errorf("%w: AWS %s", backend.ErrNotFound, code)
	}
	hints := map[string]string{ //nolint:gosec // AWS error code names and static troubleshooting hints, not credentials.
		"403":                                "access denied; the bucket may belong to another account, or your identity lacks permission (including s3:ListBucket for the ownership check)",
		"AccessDenied":                       "access denied; check the selected AWS account and IAM/bucket permissions",
		"BucketAlreadyExists":                "bucket names are globally unique; choose another bucket name",
		"BucketAlreadyOwnedByYou":            "this account already owns the bucket; retry init to verify its region and reuse it",
		"301":                                "bucket is in another region; select its actual region or use a new bucket name",
		"PermanentRedirect":                  "bucket is in another region; select its actual region or use a new bucket name",
		"400":                                "request rejected; check bucket ownership, region activation and session-token validity in opt-in regions such as eu-south-2",
		"OptInRequired":                      "enable the selected region in your AWS account settings, then retry",
		"IllegalLocationConstraintException": "bucket region differs from the request region; select the correct bucket region",
		"InvalidLocationConstraint":          "check that the selected AWS region exists and is enabled for this account",
		"AuthorizationHeaderMalformed":       "check the bucket region and AWS signing configuration",
		"InvalidToken":                       "refresh AWS credentials and check that the session token is valid in the selected opt-in region",
		"InvalidClientTokenId":               "refresh AWS credentials and enable the selected region if it requires opt-in",
		"ExpiredToken":                       "AWS credentials expired; log in again",
		"InvalidAccessKeyId":                 "select valid AWS credentials or log in again",
		"SignatureDoesNotMatch":              "check AWS credentials and signing configuration",
	}
	if hint, ok := hints[code]; ok {
		return &awsError{code: code, hint: hint}
	}
	return errors.New("AWS CLI request failed; check AWS credentials, region and bucket permissions")
}
