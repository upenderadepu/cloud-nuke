package resources

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/gruntwork-io/cloud-nuke/config"
	"github.com/gruntwork-io/cloud-nuke/logging"
	"github.com/gruntwork-io/cloud-nuke/report"
	"github.com/gruntwork-io/cloud-nuke/util"
	"github.com/gruntwork-io/go-commons/errors"
)

func (cw *CloudWatchAlarms) getAll(c context.Context, configObj config.Config) ([]*string, error) {
	var allAlarms []*string
	input := &cloudwatch.DescribeAlarmsInput{
		AlarmTypes: []types.AlarmType{types.AlarmTypeMetricAlarm, types.AlarmTypeCompositeAlarm},
	}

	paginator := cloudwatch.NewDescribeAlarmsPaginator(cw.Client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(c)
		if err != nil {
			return nil, errors.WithStackTrace(err)
		}

		for _, alarm := range page.MetricAlarms {
			if configObj.CloudWatchAlarm.ShouldInclude(config.ResourceValue{
				Name: alarm.AlarmName,
				Time: alarm.AlarmConfigurationUpdatedTimestamp,
			}) {
				allAlarms = append(allAlarms, alarm.AlarmName)
			}
		}

		for _, alarm := range page.CompositeAlarms {
			if configObj.CloudWatchAlarm.ShouldInclude(config.ResourceValue{
				Name: alarm.AlarmName,
				Time: alarm.AlarmConfigurationUpdatedTimestamp,
			}) {
				allAlarms = append(allAlarms, alarm.AlarmName)
			}
		}
	}

	return allAlarms, nil
}

func (cw *CloudWatchAlarms) nukeAll(identifiers []*string) error {
	if len(identifiers) == 0 {
		logging.Debugf("No CloudWatch Alarms to nuke in region %s", cw.Region)
		return nil
	}

	// NOTE: we don't need to do pagination here, because the pagination is handled by the caller to this function,
	// based on CloudWatchAlarm.MaxBatchSize, however we add a guard here to warn users when the batching fails and has a
	// chance of throttling AWS. Since we concurrently make one call for each identifier, we pick 100 for the limit here
	// because many APIs in AWS have a limit of 100 requests per second.
	if len(identifiers) > 100 {
		logging.Errorf("Nuking too many CloudWatch Alarms at once (100): halting to avoid hitting AWS API rate limiting")
		return TooManyCloudWatchAlarmsErr{}
	}

	logging.Debugf("Deleting CloudWatch Alarms in region %s", cw.Region)

	// If the alarm's type is composite alarm, remove the dependency by removing the rule.
	alarms, err := cw.Client.DescribeAlarms(cw.Context, &cloudwatch.DescribeAlarmsInput{
		AlarmTypes: []types.AlarmType{types.AlarmTypeMetricAlarm, types.AlarmTypeCompositeAlarm},
		AlarmNames: aws.ToStringSlice(identifiers),
	})
	if err != nil {
		logging.Debugf("[Failed] %s", err)
	}

	var compositeAlarmNames []*string
	for _, compositeAlarm := range alarms.CompositeAlarms {
		compositeAlarmNames = append(compositeAlarmNames, compositeAlarm.AlarmName)

		_, err := cw.Client.PutCompositeAlarm(cw.Context, &cloudwatch.PutCompositeAlarmInput{
			AlarmName: compositeAlarm.AlarmName,
			AlarmRule: aws.String("FALSE"),
		})
		if err != nil {
			logging.Debugf("[Failed] %s", err)
		}

		// Note: for composite alarms, we need to delete one by one according to the documentation
		// - https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_DeleteAlarms.html.
		_, err = cw.Client.DeleteAlarms(cw.Context, &cloudwatch.DeleteAlarmsInput{
			AlarmNames: []string{*compositeAlarm.AlarmName},
		})

		// Record status of this resource
		report.Record(report.Entry{
			Identifier:   aws.ToString(compositeAlarm.AlarmName),
			ResourceType: "CloudWatch Alarm",
			Error:        err,
		})
	}

	nonCompositeAlarms := util.Difference(identifiers, compositeAlarmNames)
	input := cloudwatch.DeleteAlarmsInput{AlarmNames: aws.ToStringSlice(nonCompositeAlarms)}
	_, err = cw.Client.DeleteAlarms(cw.Context, &input)

	// Record status of this resource
	e := report.BatchEntry{
		Identifiers:  aws.ToStringSlice(nonCompositeAlarms),
		ResourceType: "CloudWatch Alarm",
		Error:        err,
	}
	report.RecordBatch(e)

	if err != nil {
		logging.Debugf("[Failed] %s", err)
		return errors.WithStackTrace(err)
	}

	for _, alarmName := range identifiers {
		logging.Debugf("[OK] CloudWatch Alarm %s was deleted in %s", aws.ToString(alarmName), cw.Region)
	}
	return nil
}

// Custom errors

type TooManyCloudWatchAlarmsErr struct{}

func (err TooManyCloudWatchAlarmsErr) Error() string {
	return "Too many CloudWatch Alarms requested at once."
}
