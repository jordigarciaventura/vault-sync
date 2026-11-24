package s3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

var _ backend.ProjectInitializer = (*Backend)(nil)

// SetupFields collects the bucket region and optional AWS account check.
func (b *Backend) SetupFields() []backend.SetupField {
	region := b.region
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}
	return []backend.SetupField{
		{Key: "region", Label: "S3 bucket region (e.g. us-east-1, eu-west-1)", Default: region},
		{Key: "account", Label: "Expected AWS account ID (optional)", Default: b.account},
	}
}

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var regionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-[0-9]+$`)

// InitializeProject creates a private, empty general-purpose bucket. It never
// silently adopts a globally named bucket belonging to another account.
func (b *Backend) InitializeProject(ctx context.Context, project string) (backend.ProjectSetup, error) {
	bucket, _, err := backend.SplitProject(project)
	if err != nil {
		return backend.ProjectSetup{}, err
	}
	if !bucketPattern.MatchString(bucket) || strings.Contains(bucket, "..") || net.ParseIP(bucket) != nil || strings.HasPrefix(bucket, "xn--") || strings.HasPrefix(bucket, "sthree-") || strings.HasPrefix(bucket, "amzn-s3-demo-") || strings.HasSuffix(bucket, "-s3alias") || strings.HasSuffix(bucket, "--ol-s3") || strings.HasSuffix(bucket, ".mrap") || strings.HasSuffix(bucket, "--x-s3") || strings.HasSuffix(bucket, "--table-s3") {
		return backend.ProjectSetup{}, errors.New("invalid S3 bucket name; use 3–63 lowercase letters, digits, dots or hyphens and a globally unique name")
	}
	if !regionPattern.MatchString(b.region) {
		return backend.ProjectSetup{}, errors.New("backend.region: specify an AWS region such as us-east-1 or eu-west-1")
	}
	if err := b.Authenticate(ctx); err != nil {
		return backend.ProjectSetup{}, err
	}
	// Head first avoids CreateBucket resetting ACLs on an existing us-east-1 bucket.
	_, err = b.run(ctx, nil, "s3api", "head-bucket", "--bucket", bucket, "--expected-bucket-owner", b.account)
	if err != nil {
		if !errors.Is(err, backend.ErrNotFound) {
			var awsErr *awsError
			if errors.As(err, &awsErr) && (awsErr.code == "403" || awsErr.code == "AccessDenied") {
				return backend.ProjectSetup{}, fmt.Errorf("%w: bucket %q cannot be accessed by AWS account %s; choose a globally unique name, or verify ownership and s3:ListBucket permission: %w", backend.ErrContainerUnavailable, bucket, b.account, err)
			}
			return backend.ProjectSetup{}, fmt.Errorf("check S3 bucket %q in region %s: %w", bucket, b.region, err)
		}
		args := []string{"s3api", "create-bucket", "--bucket", bucket, "--object-ownership", "BucketOwnerEnforced"}
		if b.region != "us-east-1" {
			args = append(args, "--create-bucket-configuration", "LocationConstraint="+b.region)
		}
		if _, err := b.run(ctx, nil, args...); err != nil {
			var awsErr *awsError
			if errors.As(err, &awsErr) && awsErr.code == "BucketAlreadyExists" {
				return backend.ProjectSetup{}, fmt.Errorf("%w: bucket %q is already taken; choose a globally unique name: %w", backend.ErrContainerUnavailable, bucket, err)
			}
			return backend.ProjectSetup{}, fmt.Errorf("create S3 bucket %q in region %s: %w", bucket, b.region, err)
		}
	}
	if err == nil {
		data, locationErr := b.run(ctx, nil, "s3api", "get-bucket-location", "--bucket", bucket, "--expected-bucket-owner", b.account)
		if locationErr != nil {
			return backend.ProjectSetup{}, locationErr
		}
		var loc struct{ LocationConstraint string }
		if json.Unmarshal(data, &loc) != nil {
			return backend.ProjectSetup{}, errors.New("invalid S3 bucket location response")
		}
		actual := loc.LocationConstraint
		if actual == "" {
			actual = "us-east-1"
		}
		if actual == "EU" {
			actual = "eu-west-1"
		}
		if actual != b.region {
			return backend.ProjectSetup{}, fmt.Errorf("bucket is in %s; retry init with that region", actual)
		}
		return backend.ProjectSetup{Project: project, Config: backend.Config{Type: Type, Account: b.account, Region: b.region}}, nil
	}
	// Protect a newly created bucket without changing an existing bucket policy.
	if _, err := b.run(ctx, nil, "s3api", "put-public-access-block", "--bucket", bucket, "--expected-bucket-owner", b.account, "--public-access-block-configuration", "BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true"); err != nil {
		return backend.ProjectSetup{}, fmt.Errorf("bucket exists but configuring public access protection failed; fix permissions and retry init: %w", err)
	}
	return backend.ProjectSetup{Project: project, Config: backend.Config{Type: Type, Account: b.account, Region: b.region}}, nil
}
