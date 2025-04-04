package resources

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/gruntwork-io/cloud-nuke/config"
	"github.com/gruntwork-io/cloud-nuke/logging"
	"github.com/gruntwork-io/cloud-nuke/report"
	"github.com/gruntwork-io/cloud-nuke/util"
	"github.com/gruntwork-io/go-commons/errors"
)

func (di *DBInstances) getAll(ctx context.Context, configObj config.Config) ([]*string, error) {
	result, err := di.Client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{})
	if err != nil {
		return nil, errors.WithStackTrace(err)
	}

	var names []*string

	for _, database := range result.DBInstances {
		// instance is deletion-protected while config object doesn't include deletion-protected
		if database.DeletionProtection != nil && *database.DeletionProtection && !configObj.DBInstances.IncludeDeletionProtected {
			continue
		}

		if !configObj.DBInstances.ShouldInclude(config.ResourceValue{
			Time: database.InstanceCreateTime,
			Name: database.DBInstanceIdentifier,
			Tags: util.ConvertRDSTypeTagsToMap(database.TagList),
		}) {
			continue
		}

		names = append(names, database.DBInstanceIdentifier)
	}

	return names, nil
}

func (di *DBInstances) nukeAll(names []*string) error {
	if len(names) == 0 {
		logging.Debugf("No RDS DB Instance to nuke in region %s", di.Region)
		return nil
	}

	logging.Debugf("Deleting all RDS Instances in region %s", di.Region)
	deletedNames := []*string{}

	for _, name := range names {
		// Disable deletion protection
		_, err := di.Client.ModifyDBInstance(context.TODO(), &rds.ModifyDBInstanceInput{
			DBInstanceIdentifier: name,
			DeletionProtection:   aws.Bool(false),
			ApplyImmediately:     aws.Bool(true),
		})
		if err != nil {
			logging.Warnf("[Failed] to disable deletion protection for %s: %s", *name, err)
		}

		params := &rds.DeleteDBInstanceInput{
			DBInstanceIdentifier: name,
			SkipFinalSnapshot:    aws.Bool(true),
		}

		_, err = di.Client.DeleteDBInstance(di.Context, params)

		if err != nil {
			logging.Errorf("[Failed] %s: %s", *name, err)
		} else {
			deletedNames = append(deletedNames, name)
			logging.Debugf("Deleted RDS DB Instance: %s", aws.ToString(name))
		}
	}

	if len(deletedNames) > 0 {
		for _, name := range deletedNames {

			waiter := rds.NewDBInstanceDeletedWaiter(di.Client)
			err := waiter.Wait(di.Context, &rds.DescribeDBInstancesInput{
				DBInstanceIdentifier: name,
			}, 1*time.Minute)

			// Record status of this resource
			e := report.Entry{
				Identifier:   aws.ToString(name),
				ResourceType: "RDS Instance",
				Error:        err,
			}
			report.Record(e)

			if err != nil {
				logging.Errorf("[Failed] %s", err)
				return errors.WithStackTrace(err)
			}
		}
	}

	logging.Debugf("[OK] %d RDS DB Instance(s) deleted in %s", len(deletedNames), di.Region)
	return nil
}
