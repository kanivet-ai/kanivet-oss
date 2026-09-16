export interface IamActionGroup {
  service: string;
  label: string;
  actions: string[];
  resourceExample: string;
}

export const COMMON_IAM_ACTIONS: IamActionGroup[] = [
  {
    service: 's3',
    label: 'S3',
    resourceExample: 'arn:aws:s3:::my-bucket/*',
    actions: [
      's3:GetObject',
      's3:PutObject',
      's3:DeleteObject',
      's3:ListBucket',
      's3:GetBucketLocation',
      's3:GetObjectVersion',
    ],
  },
  {
    service: 'sqs',
    label: 'SQS',
    resourceExample: 'arn:aws:sqs:us-east-1:123456789012:my-queue',
    actions: [
      'sqs:SendMessage',
      'sqs:ReceiveMessage',
      'sqs:DeleteMessage',
      'sqs:GetQueueAttributes',
      'sqs:GetQueueUrl',
      'sqs:ChangeMessageVisibility',
    ],
  },
  {
    service: 'sns',
    label: 'SNS',
    resourceExample: 'arn:aws:sns:us-east-1:123456789012:my-topic',
    actions: ['sns:Publish', 'sns:Subscribe', 'sns:GetTopicAttributes'],
  },
  {
    service: 'dynamodb',
    label: 'DynamoDB',
    resourceExample: 'arn:aws:dynamodb:us-east-1:123456789012:table/my-table',
    actions: [
      'dynamodb:GetItem',
      'dynamodb:PutItem',
      'dynamodb:UpdateItem',
      'dynamodb:DeleteItem',
      'dynamodb:Query',
      'dynamodb:Scan',
      'dynamodb:BatchGetItem',
      'dynamodb:BatchWriteItem',
    ],
  },
  {
    service: 'secretsmanager',
    label: 'Secrets Manager',
    resourceExample:
      'arn:aws:secretsmanager:us-east-1:123456789012:secret:my-secret-*',
    actions: [
      'secretsmanager:GetSecretValue',
      'secretsmanager:DescribeSecret',
      'secretsmanager:ListSecrets',
    ],
  },
  {
    service: 'ssm',
    label: 'SSM',
    resourceExample: 'arn:aws:ssm:us-east-1:123456789012:parameter/my/param',
    actions: [
      'ssm:GetParameter',
      'ssm:GetParameters',
      'ssm:GetParametersByPath',
      'ssm:PutParameter',
    ],
  },
  {
    service: 'kms',
    label: 'KMS',
    resourceExample:
      'arn:aws:kms:us-east-1:123456789012:key/00000000-0000-0000-0000-000000000000',
    actions: [
      'kms:Decrypt',
      'kms:Encrypt',
      'kms:GenerateDataKey',
      'kms:DescribeKey',
    ],
  },
  {
    service: 'ecr',
    label: 'ECR',
    resourceExample: 'arn:aws:ecr:us-east-1:123456789012:repository/my-repo',
    actions: [
      'ecr:GetAuthorizationToken',
      'ecr:BatchGetImage',
      'ecr:GetDownloadUrlForLayer',
      'ecr:BatchCheckLayerAvailability',
      'ecr:PutImage',
    ],
  },
  {
    service: 'sts',
    label: 'STS',
    resourceExample: 'arn:aws:iam::123456789012:role/target-role',
    actions: ['sts:AssumeRole', 'sts:GetCallerIdentity', 'sts:TagSession'],
  },
  {
    service: 'logs',
    label: 'CloudWatch Logs',
    resourceExample: 'arn:aws:logs:us-east-1:123456789012:log-group:/app/*',
    actions: [
      'logs:CreateLogGroup',
      'logs:CreateLogStream',
      'logs:PutLogEvents',
      'logs:DescribeLogStreams',
    ],
  },
  {
    service: 'cloudwatch',
    label: 'CloudWatch',
    resourceExample: '*',
    actions: ['cloudwatch:PutMetricData', 'cloudwatch:GetMetricData'],
  },
  {
    service: 'rds',
    label: 'RDS',
    resourceExample: 'arn:aws:rds-db:us-east-1:123456789012:dbuser:db-ABC/app',
    actions: ['rds-db:connect', 'rds:DescribeDBInstances'],
  },
  {
    service: 'lambda',
    label: 'Lambda',
    resourceExample: 'arn:aws:lambda:us-east-1:123456789012:function:my-fn',
    actions: ['lambda:InvokeFunction', 'lambda:GetFunction'],
  },
  {
    service: 'events',
    label: 'EventBridge',
    resourceExample: 'arn:aws:events:us-east-1:123456789012:event-bus/default',
    actions: ['events:PutEvents'],
  },
  {
    service: 'kinesis',
    label: 'Kinesis',
    resourceExample: 'arn:aws:kinesis:us-east-1:123456789012:stream/my-stream',
    actions: [
      'kinesis:PutRecord',
      'kinesis:PutRecords',
      'kinesis:GetRecords',
      'kinesis:GetShardIterator',
      'kinesis:DescribeStream',
    ],
  },
];

export const ALL_IAM_ACTIONS: string[] = COMMON_IAM_ACTIONS.flatMap(
  (g) => g.actions,
);

export function resourceExampleForAction(action: string): string {
  const service = action.split(':')[0];
  return (
    COMMON_IAM_ACTIONS.find((g) => g.service === service)?.resourceExample ||
    'arn:aws:...'
  );
}
