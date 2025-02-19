package resources

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3control"
	"github.com/aws/aws-sdk-go-v2/service/s3control/types"
	"github.com/gruntwork-io/cloud-nuke/config"
	"github.com/gruntwork-io/cloud-nuke/logging"
	"github.com/gruntwork-io/cloud-nuke/report"
	"github.com/gruntwork-io/cloud-nuke/util"
	"github.com/gruntwork-io/go-commons/errors"
)

func (ap *S3MultiRegionAccessPoint) getAll(c context.Context, configObj config.Config) ([]*string, error) {
	// NOTE: All control plane requests to create or maintain Multi-Region Access Points must be routed to the US West (Oregon) Region.
	// Reference: https://docs.aws.amazon.com/AmazonS3/latest/userguide/MultiRegionAccessPointRestrictions.html
	//
	// To avoid receiving the error `PermanentRedirect: This API operation is only available in the following Regions: us-west-2. Make sure to send all future requests to a supported Region`,
	// we must ensure that the region is set to us-west-2.
	if ap.Region != "us-west-2" {
		logging.Debugf("Listing Multi-Region Access Points is only available in the following Region: us-west-2.")
		return nil, nil
	}

	accountID, ok := c.Value(util.AccountIdKey).(string)
	if !ok {
		logging.Errorf("unable to read the account-id from context")
		return nil, errors.WithStackTrace(fmt.Errorf("unable to lookup the account id"))
	}

	// set the account id in object as this is mandatory to nuke an access point
	ap.AccountID = aws.String(accountID)

	var accessPoints []*string
	paginator := s3control.NewListMultiRegionAccessPointsPaginator(ap.Client, &s3control.ListMultiRegionAccessPointsInput{
		AccountId: ap.AccountID,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ap.Context)
		if err != nil {
			return nil, errors.WithStackTrace(err)
		}

		for _, accessPoint := range page.AccessPoints {
			if configObj.S3MultiRegionAccessPoint.ShouldInclude(config.ResourceValue{
				Name: accessPoint.Name,
				Time: accessPoint.CreatedAt,
			}) {
				accessPoints = append(accessPoints, accessPoint.Name)
			}
		}
	}

	return accessPoints, nil
}

func (ap *S3MultiRegionAccessPoint) nukeAll(identifiers []*string) error {
	if len(identifiers) == 0 {
		logging.Debugf("No Multi region access point(s) to nuke in region %s", ap.Region)
		return nil
	}

	logging.Debugf("Deleting all Multi region access points in region %s", ap.Region)
	var deleted []*string

	for _, id := range identifiers {

		_, err := ap.Client.DeleteMultiRegionAccessPoint(
			ap.Context,
			&s3control.DeleteMultiRegionAccessPointInput{
				AccountId: ap.AccountID,
				Details: &types.DeleteMultiRegionAccessPointInput{
					Name: id,
				},
			})

		// Record status of this resource
		e := report.Entry{
			Identifier:   aws.ToString(id),
			ResourceType: "S3 Multi Region Access point",
			Error:        err,
		}
		report.Record(e)

		if err != nil {
			logging.Debugf("[Failed] %s", err)
		} else {
			deleted = append(deleted, id)
			logging.Debugf("Deleted S3 Multi region access point: %s", aws.ToString(id))
		}
	}

	logging.Debugf("[OK] %d S3 Multi region access point(s) deleted in %s", len(deleted), ap.Region)

	return nil
}
