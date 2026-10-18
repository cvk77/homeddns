//go:build aws_route53 || (!netcup_ccp && !aws_route53)

package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/markussiebert/homeddns/internal/logger"
)

// Route53API defines the interface for the AWS Route53 API, enabling mocking.
type Route53API interface {
	ListHostedZones(ctx context.Context, params *route53.ListHostedZonesInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error)
	ListResourceRecordSets(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
	ChangeResourceRecordSets(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
}

// AwsRoute53Client represents an AWS Route53 client
type AwsRoute53Client struct {
	client    Route53API
	zoneID    string // fixed hosted zone ID (ROUTE53_ZONE_ID); skips zone discovery when set
	zoneMu    sync.RWMutex
	zoneCache map[string]string // hostname -> hostedZoneId cache
}

// NewAwsRoute53Client creates a new Route53 client
func NewAwsRoute53Client(ctx context.Context) (*AwsRoute53Client, error) {
	logger.Debug("Loading AWS Route53 configuration")

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		logger.Info("Ensure AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, and AWS_REGION are set")
		return nil, logger.Errorf("load AWS config: %w", err)
	}

	logger.Info("AWS Route53 client initialized successfully (region: %s)", cfg.Region)
	logger.Debug("AWS config loaded from default credential chain")

	return &AwsRoute53Client{
		client:    route53.NewFromConfig(cfg),
		zoneCache: make(map[string]string),
	}, nil
}

// NewAwsRoute53ClientWithConfig creates a new Route53 client with custom AWS config
func NewAwsRoute53ClientWithConfig(cfg aws.Config) *AwsRoute53Client {
	return &AwsRoute53Client{
		client:    route53.NewFromConfig(cfg),
		zoneCache: make(map[string]string),
	}
}

// WithZoneID pins the client to a hosted zone and disables zone discovery.
func (c *AwsRoute53Client) WithZoneID(zoneID string) *AwsRoute53Client {
	c.zoneID = strings.TrimPrefix(zoneID, "/hostedzone/")
	return c
}

// NewAwsRoute53ClientWithMock creates a new Route53 client with a mock API for testing
func NewAwsRoute53ClientWithMock(mock Route53API) *AwsRoute53Client {
	return &AwsRoute53Client{
		client:    mock,
		zoneCache: make(map[string]string),
	}
}

// Name returns the provider name
func (c *AwsRoute53Client) Name() string {
	return "aws_route53"
}

// GetRecord retrieves a specific DNS record.
// The hosted zone is determined from the hostname; the domain argument is
// only used for logging.
func (c *AwsRoute53Client) GetRecord(ctx context.Context, domain, hostname, recordType string) (*DNSRecord, error) {
	logger.Debug("AWS Route53: Getting record for domain=%s, hostname=%s, type=%s", domain, hostname, recordType)

	// Get hosted zone ID
	zoneID, err := c.getHostedZoneID(ctx, hostname)
	if err != nil {
		return nil, fmt.Errorf("get hosted zone: %w", err)
	}

	recordSet, err := c.findRecordSet(ctx, zoneID, hostname, recordType)
	if err != nil {
		return nil, err
	}

	// Extract value
	var value string
	if len(recordSet.ResourceRecords) > 0 {
		value = aws.ToString(recordSet.ResourceRecords[0].Value)
	}

	return &DNSRecord{
		Name:  hostname,
		Type:  recordType,
		Value: value,
		TTL:   int(aws.ToInt64(recordSet.TTL)),
	}, nil
}

// findRecordSet returns the record set with exactly the given name and type.
func (c *AwsRoute53Client) findRecordSet(ctx context.Context, zoneID, hostname, recordType string) (*types.ResourceRecordSet, error) {
	// Ensure hostname ends with a dot for Route53
	fqdn := c.ensureTrailingDot(hostname)

	input := &route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(zoneID),
		StartRecordName: aws.String(fqdn),
		StartRecordType: types.RRType(recordType),
		MaxItems:        aws.Int32(1),
	}

	result, err := c.client.ListResourceRecordSets(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("list record sets: %w", err)
	}

	if len(result.ResourceRecordSets) == 0 {
		return nil, ErrRecordNotFound
	}

	recordSet := result.ResourceRecordSets[0]
	if !strings.EqualFold(normalizeRecordName(aws.ToString(recordSet.Name)), normalizeRecordName(fqdn)) || string(recordSet.Type) != recordType {
		return nil, ErrRecordNotFound
	}
	return &recordSet, nil
}

// normalizeRecordName converts Route53's octal escape for "*" back to the
// literal character so wildcard names compare equal.
func normalizeRecordName(name string) string {
	return strings.ReplaceAll(name, `\052`, "*")
}

// UpdateRecord updates or creates a DNS record
func (c *AwsRoute53Client) UpdateRecord(ctx context.Context, domain string, record *DNSRecord) (bool, error) {
	logger.Debug("AWS Route53: Updating record for domain=%s, name=%s, type=%s, value=%s", domain, record.Name, record.Type, record.Value)

	// Get hosted zone ID
	zoneID, err := c.getHostedZoneID(ctx, record.Name)
	if err != nil {
		return false, fmt.Errorf("get hosted zone: %w", err)
	}

	// Ensure hostname ends with a dot for Route53
	fqdn := c.ensureTrailingDot(record.Name)

	// Check if record exists and if it needs updating
	existing, err := c.GetRecord(ctx, domain, record.Name, record.Type)
	if err == nil && existing.Value == record.Value {
		logger.Debug("AWS Route53: Record already up to date")
		// Record exists and is already up to date
		return false, nil
	}

	// Prepare the resource record
	resourceRecord := types.ResourceRecord{
		Value: aws.String(record.Value),
	}

	// Prepare the change batch
	change := types.Change{
		Action: types.ChangeActionUpsert,
		ResourceRecordSet: &types.ResourceRecordSet{
			Name: aws.String(fqdn),
			Type: types.RRType(record.Type),
			TTL:  aws.Int64(int64(record.TTL)),
			ResourceRecords: []types.ResourceRecord{
				resourceRecord,
			},
		},
	}

	// Apply the change
	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{change},
		},
	}

	_, err = c.client.ChangeResourceRecordSets(ctx, input)
	if err != nil {
		return false, fmt.Errorf("change resource record sets: %w", err)
	}

	logger.Info("AWS Route53: Successfully updated record %s to %s", record.Name, record.Value)
	return true, nil
}

// DeleteRecord deletes a DNS record if it still has the given value
func (c *AwsRoute53Client) DeleteRecord(ctx context.Context, domain string, record *DNSRecord) error {
	logger.Debug("AWS Route53: Deleting record for domain=%s, name=%s, type=%s, value=%s", domain, record.Name, record.Type, record.Value)

	zoneID, err := c.getHostedZoneID(ctx, record.Name)
	if err != nil {
		return fmt.Errorf("get hosted zone: %w", err)
	}

	recordSet, err := c.findRecordSet(ctx, zoneID, record.Name, record.Type)
	if err != nil {
		return err
	}

	found := false
	for _, rr := range recordSet.ResourceRecords {
		if aws.ToString(rr.Value) == record.Value {
			found = true
			break
		}
	}
	if !found {
		return ErrRecordNotFound
	}

	// Route53 requires the delete to match the existing record set exactly.
	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{{
				Action:            types.ChangeActionDelete,
				ResourceRecordSet: recordSet,
			}},
		},
	}
	if _, err := c.client.ChangeResourceRecordSets(ctx, input); err != nil {
		return fmt.Errorf("change resource record sets: %w", err)
	}

	logger.Info("AWS Route53: Deleted record %s %s %s", record.Name, record.Type, record.Value)
	return nil
}

// Close cleans up resources (no-op for Route53)
func (c *AwsRoute53Client) Close(ctx context.Context) error {
	return nil
}

// getHostedZoneID returns the hosted zone for a hostname: the public zone
// with the longest name that is a suffix of the hostname. A zone ID
// configured via ROUTE53_ZONE_ID takes precedence.
func (c *AwsRoute53Client) getHostedZoneID(ctx context.Context, hostname string) (string, error) {
	if c.zoneID != "" {
		return c.zoneID, nil
	}

	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))

	// Check cache first
	c.zoneMu.RLock()
	zoneID, exists := c.zoneCache[hostname]
	c.zoneMu.RUnlock()
	if exists {
		logger.Debug("AWS Route53: Using cached zone ID for %s", hostname)
		return zoneID, nil
	}

	logger.Debug("AWS Route53: Looking up hosted zone ID for %s", hostname)

	var bestName, bestID string

	// List hosted zones manually for mockability
	var marker *string
	for {
		input := &route53.ListHostedZonesInput{
			Marker: marker,
		}

		output, err := c.client.ListHostedZones(ctx, input)
		if err != nil {
			return "", fmt.Errorf("list hosted zones: %w", err)
		}

		for _, zone := range output.HostedZones {
			if zone.Config != nil && zone.Config.PrivateZone {
				continue
			}
			zoneName := strings.ToLower(strings.TrimSuffix(aws.ToString(zone.Name), "."))
			if hostname != zoneName && !strings.HasSuffix(hostname, "."+zoneName) {
				continue
			}
			if len(zoneName) > len(bestName) {
				bestName = zoneName
				bestID = strings.TrimPrefix(aws.ToString(zone.Id), "/hostedzone/")
			}
		}

		if !output.IsTruncated {
			break
		}
		marker = output.NextMarker
	}

	if bestID == "" {
		return "", fmt.Errorf("no public hosted zone found for %s", hostname)
	}

	logger.Debug("AWS Route53: Using hosted zone %s (%s) for %s", bestName, bestID, hostname)
	c.zoneMu.Lock()
	c.zoneCache[hostname] = bestID
	c.zoneMu.Unlock()
	return bestID, nil
}

// ensureTrailingDot ensures the hostname ends with a dot
func (c *AwsRoute53Client) ensureTrailingDot(hostname string) string {
	if !strings.HasSuffix(hostname, ".") {
		return hostname + "."
	}
	return hostname
}

func init() {
	RegisterFactory("aws_route53", NewRoute53Provider)
}

// NewRoute53Provider creates a new AWS Route53 provider
// It uses the default AWS SDK configuration chain (env vars, ~/.aws/credentials, IAM roles, etc.)
// If ROUTE53_ZONE_ID is set, all records are written to that hosted zone.
func NewRoute53Provider(ctx context.Context, config interface{}) (Provider, error) {
	client, err := NewAwsRoute53Client(ctx)
	if err != nil {
		return nil, err
	}
	if zoneID := os.Getenv("ROUTE53_ZONE_ID"); zoneID != "" {
		logger.Info("AWS Route53: Using fixed hosted zone %s (ROUTE53_ZONE_ID)", zoneID)
		client.WithZoneID(zoneID)
	}
	return client, nil
}
