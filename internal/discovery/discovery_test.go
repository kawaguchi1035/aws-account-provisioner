package discovery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
)

func TestLoad(t *testing.T) {
	catalog, err := Load(context.Background(), workingDeps())
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if catalog.InstanceARN != "arn:aws:sso:::instance/ssoins-123" {
		t.Errorf("InstanceARN = %q", catalog.InstanceARN)
	}
	if catalog.IdentityStoreID != "d-1234567890" {
		t.Errorf("IdentityStoreID = %q", catalog.IdentityStoreID)
	}
	if catalog.AccountFactoryProductID != "prod-af" {
		t.Errorf("AccountFactoryProductID = %q, want the Account Factory product", catalog.AccountFactoryProductID)
	}

	// Permission sets span two pages; both must be resolved to names.
	for _, name := range []string{"AdministratorAccess", "ReadOnlyAccess"} {
		if _, ok := catalog.PermissionSetARN(name); !ok {
			t.Errorf("permission set %q was not loaded", name)
		}
	}

	if id, ok := catalog.PrincipalID("GROUP", "Developers"); !ok || id != "g-1" {
		t.Errorf("group Developers = %q, %v", id, ok)
	}
	if id, ok := catalog.PrincipalID("USER", "taro"); !ok || id != "u-1" {
		t.Errorf("user taro = %q, %v", id, ok)
	}
	if _, ok := catalog.PrincipalID("ROLE", "Developers"); ok {
		t.Error("PrincipalID() accepted an unknown principal type")
	}
}

func TestLoadWalksNestedOUs(t *testing.T) {
	catalog, err := Load(context.Background(), workingDeps())
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if name, ok := catalog.OUName("ou-abcd-12345678"); !ok || name != "Sandbox" {
		t.Errorf("top-level OU = %q, %v", name, ok)
	}
	// The nested OU is only reachable by recursing into its parent.
	if name, ok := catalog.OUName("ou-abcd-87654321"); !ok || name != "Nested" {
		t.Errorf("nested OU = %q, %v, want it to be discovered", name, ok)
	}
}

func TestLoadResolvesEachPermissionSetOnce(t *testing.T) {
	deps := workingDeps()
	sso := deps.SSOAdmin.(*stubSSOAdmin)

	catalog, err := Load(context.Background(), deps)
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if sso.describeCalls != 2 {
		t.Errorf("DescribePermissionSet called %d times, want 2 (one per permission set)", sso.describeCalls)
	}

	// Repeated lookups must not go back to AWS.
	for i := 0; i < 5; i++ {
		catalog.PermissionSetARN("AdministratorAccess")
	}
	if sso.describeCalls != 2 {
		t.Errorf("DescribePermissionSet called %d times after lookups, want it to stay at 2", sso.describeCalls)
	}
}

func TestLoadFailures(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Deps)
		wantMsg string
	}{
		{
			name: "no Identity Center instance",
			mutate: func(d *Deps) {
				d.SSOAdmin.(*stubSSOAdmin).instances = nil
			},
			wantMsg: "no IAM Identity Center instance",
		},
		{
			name: "more than one Identity Center instance",
			mutate: func(d *Deps) {
				s := d.SSOAdmin.(*stubSSOAdmin)
				s.instances = append(s.instances, ssotypes.InstanceMetadata{
					InstanceArn:     aws.String("arn:aws:sso:::instance/ssoins-456"),
					IdentityStoreId: aws.String("d-0987654321"),
				})
			},
			wantMsg: "found 2 IAM Identity Center instances",
		},
		{
			name: "Account Factory product missing",
			mutate: func(d *Deps) {
				d.ServiceCatalog.(*stubServiceCatalog).products = nil
			},
			wantMsg: "not found",
		},
		{
			name: "listing instances fails",
			mutate: func(d *Deps) {
				d.SSOAdmin.(*stubSSOAdmin).listInstanceErr = errors.New("access denied")
			},
			wantMsg: "list IAM Identity Center instances",
		},
		{
			name: "listing permission sets fails",
			mutate: func(d *Deps) {
				d.SSOAdmin.(*stubSSOAdmin).listErr = errors.New("access denied")
			},
			wantMsg: "list permission sets",
		},
		{
			name: "describing a permission set fails",
			mutate: func(d *Deps) {
				d.SSOAdmin.(*stubSSOAdmin).describeErrFor = "arn:ps/admin"
			},
			wantMsg: "describe permission set arn:ps/admin",
		},
		{
			name: "listing users fails",
			mutate: func(d *Deps) {
				d.IdentityStore.(*stubIdentityStore).usersErr = errors.New("access denied")
			},
			wantMsg: "list Identity Center users",
		},
		{
			name: "listing roots fails",
			mutate: func(d *Deps) {
				d.Organizations.(*stubOrganizations).rootsErr = errors.New("access denied")
			},
			wantMsg: "list organization roots",
		},
		{
			name: "searching products fails",
			mutate: func(d *Deps) {
				d.ServiceCatalog.(*stubServiceCatalog).err = errors.New("access denied")
			},
			wantMsg: "search for the Account Factory product",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := workingDeps()
			tt.mutate(&deps)

			_, err := Load(context.Background(), deps)
			if err == nil {
				t.Fatal("Load() = nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q does not mention %q", err, tt.wantMsg)
			}
		})
	}
}
