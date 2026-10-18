//go:build aws_route53 || (!netcup_ccp && !aws_route53)

package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// mockRoute53API satisfies the Route53API interface and allows controlling the responses.
type mockRoute53API struct {
	ListHostedZonesFunc          func(ctx context.Context, params *route53.ListHostedZonesInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error)
	ListResourceRecordSetsFunc   func(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
	ChangeResourceRecordSetsFunc func(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
}

func (m *mockRoute53API) ListHostedZones(ctx context.Context, params *route53.ListHostedZonesInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
	if m.ListHostedZonesFunc != nil {
		return m.ListHostedZonesFunc(ctx, params, optFns...)
	}
	return nil, fmt.Errorf("ListHostedZonesFunc is not implemented")
}

func (m *mockRoute53API) ListResourceRecordSets(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
	if m.ListResourceRecordSetsFunc != nil {
		return m.ListResourceRecordSetsFunc(ctx, params, optFns...)
	}
	return nil, fmt.Errorf("ListResourceRecordSetsFunc is not implemented")
}

func (m *mockRoute53API) ChangeResourceRecordSets(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
	if m.ChangeResourceRecordSetsFunc != nil {
		return m.ChangeResourceRecordSetsFunc(ctx, params, optFns...)
	}
	return nil, fmt.Errorf("ChangeResourceRecordSetsFunc is not implemented")
}

func TestAwsRoute53Client_GetRecord(t *testing.T) {
	domain := "example.com"
	hostname := "test.example.com"
	recordType := "A"
	expectedValue := "192.0.2.1"

	mockAPI := &mockRoute53API{
		ListHostedZonesFunc: func(ctx context.Context, params *route53.ListHostedZonesInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
			return &route53.ListHostedZonesOutput{
				HostedZones: []types.HostedZone{{
					Id:   aws.String("/hostedzone/ZONE123"),
					Name: aws.String("example.com."),
				}},
				IsTruncated: false,
			}, nil
		},
		ListResourceRecordSetsFunc: func(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
			zoneID := aws.ToString(params.HostedZoneId)
			if zoneID != "ZONE123" {
				t.Fatalf("expected hosted zone %s, got %s", "ZONE123", zoneID)
			}
			if aws.ToString(params.StartRecordName) != "test.example.com." {
				t.Fatalf("unexpected start record name: %s", aws.ToString(params.StartRecordName))
			}
			if params.StartRecordType != types.RRTypeA {
				t.Fatalf("unexpected record type: %s", params.StartRecordType)
			}

			return &route53.ListResourceRecordSetsOutput{
				ResourceRecordSets: []types.ResourceRecordSet{{
					Name:            aws.String("test.example.com."),
					Type:            types.RRTypeA,
					TTL:             aws.Int64(300),
					ResourceRecords: []types.ResourceRecord{{Value: aws.String(expectedValue)}},
				}},
			}, nil
		},
	}

	client := NewAwsRoute53ClientWithMock(mockAPI)
	record, err := client.GetRecord(context.Background(), domain, hostname, recordType)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if record == nil {
		t.Fatalf("expected record")
	}
	if record.Name != hostname {
		t.Fatalf("unexpected name: %s", record.Name)
	}
	if record.Type != recordType {
		t.Fatalf("unexpected type: %s", record.Type)
	}
	if record.Value != expectedValue {
		t.Fatalf("unexpected value: %s", record.Value)
	}
	if record.TTL != 300 {
		t.Fatalf("unexpected ttl: %d", record.TTL)
	}
}

func TestAwsRoute53Client_UpdateRecord(t *testing.T) {
	domain := "example.com"
	record := &DNSRecord{
		Name:  "test.example.com",
		Type:  "A",
		Value: "192.0.2.2",
		TTL:   250,
	}

	mockAPI := &mockRoute53API{}
	var changeCalls int

	mockAPI.ListHostedZonesFunc = func(ctx context.Context, params *route53.ListHostedZonesInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
		return &route53.ListHostedZonesOutput{HostedZones: []types.HostedZone{{
			Id:   aws.String("/hostedzone/ZONE123"),
			Name: aws.String("example.com."),
		}}}, nil
	}

	mockAPI.ListResourceRecordSetsFunc = func(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
		return &route53.ListResourceRecordSetsOutput{ResourceRecordSets: []types.ResourceRecordSet{}}, nil
	}

	mockAPI.ChangeResourceRecordSetsFunc = func(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
		changeCalls++
		if aws.ToString(params.HostedZoneId) != "ZONE123" {
			t.Fatalf("unexpected hosted zone: %s", aws.ToString(params.HostedZoneId))
		}
		if len(params.ChangeBatch.Changes) != 1 {
			t.Fatalf("expected a single change, got %d", len(params.ChangeBatch.Changes))
		}
		change := params.ChangeBatch.Changes[0]
		if change.Action != types.ChangeActionUpsert {
			t.Fatalf("unexpected action: %s", change.Action)
		}
		if aws.ToString(change.ResourceRecordSet.Name) != "test.example.com." {
			t.Fatalf("unexpected record name: %s", aws.ToString(change.ResourceRecordSet.Name))
		}
		if change.ResourceRecordSet.Type != types.RRTypeA {
			t.Fatalf("unexpected record type: %s", change.ResourceRecordSet.Type)
		}
		if aws.ToInt64(change.ResourceRecordSet.TTL) != int64(record.TTL) {
			t.Fatalf("unexpected ttl %d", aws.ToInt64(change.ResourceRecordSet.TTL))
		}
		if aws.ToString(change.ResourceRecordSet.ResourceRecords[0].Value) != record.Value {
			t.Fatalf("unexpected value: %s", aws.ToString(change.ResourceRecordSet.ResourceRecords[0].Value))
		}
		return &route53.ChangeResourceRecordSetsOutput{}, nil
	}

	client := NewAwsRoute53ClientWithMock(mockAPI)
	changed, err := client.UpdateRecord(context.Background(), domain, record)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !changed {
		t.Fatalf("expected record to be reported as changed")
	}
	if changeCalls != 1 {
		t.Fatalf("expected 1 change call, got %d", changeCalls)
	}
}

func TestAwsRoute53Client_ConcurrentZoneLookup(t *testing.T) {
	mockAPI := &mockRoute53API{
		ListHostedZonesFunc: func(ctx context.Context, params *route53.ListHostedZonesInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
			return &route53.ListHostedZonesOutput{HostedZones: []types.HostedZone{{
				Id:   aws.String("/hostedzone/ZONE123"),
				Name: aws.String("example.com."),
			}}}, nil
		},
	}
	client := NewAwsRoute53ClientWithMock(mockAPI)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			zoneID, err := client.getHostedZoneID(context.Background(), "example.com")
			if err != nil || zoneID != "ZONE123" {
				t.Errorf("unexpected result: %q, %v", zoneID, err)
			}
		}()
	}
	wg.Wait()
}

func TestAwsRoute53Client_UpdateRecordUnchanged(t *testing.T) {
	mockAPI := &mockRoute53API{
		ListHostedZonesFunc: func(ctx context.Context, params *route53.ListHostedZonesInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
			return &route53.ListHostedZonesOutput{HostedZones: []types.HostedZone{{
				Id:   aws.String("/hostedzone/ZONE123"),
				Name: aws.String("example.com."),
			}}}, nil
		},
		ListResourceRecordSetsFunc: func(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
			return &route53.ListResourceRecordSetsOutput{ResourceRecordSets: []types.ResourceRecordSet{{
				Name:            aws.String("test.example.com."),
				Type:            types.RRTypeA,
				TTL:             aws.Int64(60),
				ResourceRecords: []types.ResourceRecord{{Value: aws.String("192.0.2.2")}},
			}}}, nil
		},
		ChangeResourceRecordSetsFunc: func(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
			t.Fatalf("unexpected change call for unchanged record")
			return nil, nil
		},
	}

	client := NewAwsRoute53ClientWithMock(mockAPI)
	changed, err := client.UpdateRecord(context.Background(), "example.com", &DNSRecord{
		Name: "test.example.com", Type: "A", Value: "192.0.2.2", TTL: 60,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if changed {
		t.Fatalf("expected record to be reported as unchanged")
	}
}

func zonesMock(zones ...types.HostedZone) *mockRoute53API {
	return &mockRoute53API{
		ListHostedZonesFunc: func(ctx context.Context, params *route53.ListHostedZonesInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
			return &route53.ListHostedZonesOutput{HostedZones: zones}, nil
		},
	}
}

func zone(id, name string, private bool) types.HostedZone {
	return types.HostedZone{
		Id:     aws.String("/hostedzone/" + id),
		Name:   aws.String(name),
		Config: &types.HostedZoneConfig{PrivateZone: private},
	}
}

func TestAwsRoute53Client_ZoneDiscovery(t *testing.T) {
	mockAPI := zonesMock(
		zone("COUK", "example.co.uk.", false),
		zone("COM", "example.com.", false),
		zone("DYN", "dyn.example.com.", false),
		zone("PRIVATE", "home.example.com.", true),
		zone("OTHER", "ample.com.", false),
	)

	testCases := []struct {
		hostname string
		expected string
	}{
		{"home.example.co.uk", "COUK"},
		{"example.com", "COM"},
		{"www.example.com", "COM"},
		{"a.dyn.example.com", "DYN"},
		{"*.dyn.example.com", "DYN"},
		{"dyn.example.com.", "DYN"},
		{"home.example.com", "COM"}, // private zone is ignored
	}

	client := NewAwsRoute53ClientWithMock(mockAPI)
	for _, tc := range testCases {
		t.Run(tc.hostname, func(t *testing.T) {
			zoneID, err := client.getHostedZoneID(context.Background(), tc.hostname)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if zoneID != tc.expected {
				t.Fatalf("expected zone %s, got %s", tc.expected, zoneID)
			}
		})
	}

	if _, err := client.getHostedZoneID(context.Background(), "example.org"); err == nil {
		t.Fatalf("expected error for hostname without zone")
	}
}

func TestAwsRoute53Client_FixedZoneID(t *testing.T) {
	mockAPI := &mockRoute53API{} // ListHostedZones must not be called
	client := NewAwsRoute53ClientWithMock(mockAPI).WithZoneID("/hostedzone/FIXED")

	zoneID, err := client.getHostedZoneID(context.Background(), "home.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if zoneID != "FIXED" {
		t.Fatalf("expected FIXED, got %s", zoneID)
	}
}

func TestAwsRoute53Client_WildcardUnchanged(t *testing.T) {
	mockAPI := zonesMock(zone("ZONE123", "example.com.", false))
	mockAPI.ListResourceRecordSetsFunc = func(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
		return &route53.ListResourceRecordSetsOutput{ResourceRecordSets: []types.ResourceRecordSet{{
			Name:            aws.String(`\052.example.com.`),
			Type:            types.RRTypeAaaa,
			TTL:             aws.Int64(60),
			ResourceRecords: []types.ResourceRecord{{Value: aws.String("2001:db8::1")}},
		}}}, nil
	}
	mockAPI.ChangeResourceRecordSetsFunc = func(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
		t.Fatalf("unexpected change call for unchanged wildcard record")
		return nil, nil
	}

	client := NewAwsRoute53ClientWithMock(mockAPI)
	changed, err := client.UpdateRecord(context.Background(), "example.com", &DNSRecord{
		Name: "*.example.com", Type: "AAAA", Value: "2001:db8::1", TTL: 60,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatalf("expected wildcard record to be reported as unchanged")
	}
}

func TestAwsRoute53Client_DeleteRecord(t *testing.T) {
	existing := types.ResourceRecordSet{
		Name:            aws.String("home.example.com."),
		Type:            types.RRTypeA,
		TTL:             aws.Int64(300),
		ResourceRecords: []types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
	}
	mockAPI := zonesMock(zone("ZONE123", "example.com.", false))
	mockAPI.ListResourceRecordSetsFunc = func(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
		return &route53.ListResourceRecordSetsOutput{ResourceRecordSets: []types.ResourceRecordSet{existing}}, nil
	}
	var deleted *types.ResourceRecordSet
	mockAPI.ChangeResourceRecordSetsFunc = func(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
		change := params.ChangeBatch.Changes[0]
		if change.Action != types.ChangeActionDelete {
			t.Fatalf("unexpected action: %s", change.Action)
		}
		deleted = change.ResourceRecordSet
		return &route53.ChangeResourceRecordSetsOutput{}, nil
	}
	client := NewAwsRoute53ClientWithMock(mockAPI)

	// Value mismatch: nothing is deleted
	err := client.DeleteRecord(context.Background(), "", &DNSRecord{Name: "home.example.com", Type: "A", Value: "192.0.2.9"})
	if !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound, got %v", err)
	}
	if deleted != nil {
		t.Fatalf("unexpected delete")
	}

	err = client.DeleteRecord(context.Background(), "", &DNSRecord{Name: "home.example.com", Type: "A", Value: "192.0.2.1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted == nil || aws.ToInt64(deleted.TTL) != 300 || aws.ToString(deleted.Name) != "home.example.com." {
		t.Fatalf("delete must match the existing record set exactly, got %+v", deleted)
	}
}
