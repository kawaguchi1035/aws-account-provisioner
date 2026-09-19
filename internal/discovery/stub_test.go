package discovery

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	idtypes "github.com/aws/aws-sdk-go-v2/service/identitystore/types"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/servicecatalog"
	sctypes "github.com/aws/aws-sdk-go-v2/service/servicecatalog/types"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
)

// page models one paginated response: the items plus the token that follows it.
type page[T any] struct {
	items []T
	next  string
}

func tokenOf(p page[string]) *string { return nextToken(p.next) }

func nextToken(s string) *string {
	if s == "" {
		return nil
	}
	return aws.String(s)
}

type stubSSOAdmin struct {
	instances       []ssotypes.InstanceMetadata
	permissionSets  []page[string]
	names           map[string]string // ARN -> name
	listCalls       int
	describeCalls   int
	listErr         error
	describeErrFor  string
	listInstanceErr error
}

func (s *stubSSOAdmin) ListInstances(context.Context, *ssoadmin.ListInstancesInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListInstancesOutput, error) {
	if s.listInstanceErr != nil {
		return nil, s.listInstanceErr
	}
	return &ssoadmin.ListInstancesOutput{Instances: s.instances}, nil
}

func (s *stubSSOAdmin) ListPermissionSets(_ context.Context, in *ssoadmin.ListPermissionSetsInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.ListPermissionSetsOutput, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	s.listCalls++
	p := s.pageFor(aws.ToString(in.NextToken))
	return &ssoadmin.ListPermissionSetsOutput{PermissionSets: p.items, NextToken: tokenOf(p)}, nil
}

// pageFor returns the first page for an empty token, otherwise the page whose
// predecessor handed out that token.
func (s *stubSSOAdmin) pageFor(token string) page[string] {
	if token == "" {
		return s.permissionSets[0]
	}
	for i, p := range s.permissionSets {
		if p.next == token && i+1 < len(s.permissionSets) {
			return s.permissionSets[i+1]
		}
	}
	return page[string]{}
}

func (s *stubSSOAdmin) DescribePermissionSet(_ context.Context, in *ssoadmin.DescribePermissionSetInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.DescribePermissionSetOutput, error) {
	arn := aws.ToString(in.PermissionSetArn)
	if s.describeErrFor != "" && s.describeErrFor == arn {
		return nil, errors.New("describe failed")
	}
	s.describeCalls++
	name, ok := s.names[arn]
	if !ok {
		return &ssoadmin.DescribePermissionSetOutput{}, nil
	}
	return &ssoadmin.DescribePermissionSetOutput{
		PermissionSet: &ssotypes.PermissionSet{Name: aws.String(name)},
	}, nil
}

type stubIdentityStore struct {
	users    []page[idtypes.User]
	groups   []page[idtypes.Group]
	usersErr error
}

func (s *stubIdentityStore) ListUsers(_ context.Context, in *identitystore.ListUsersInput, _ ...func(*identitystore.Options)) (*identitystore.ListUsersOutput, error) {
	if s.usersErr != nil {
		return nil, s.usersErr
	}
	p := pageFor(s.users, aws.ToString(in.NextToken))
	return &identitystore.ListUsersOutput{Users: p.items, NextToken: nextToken(p.next)}, nil
}

func (s *stubIdentityStore) ListGroups(_ context.Context, in *identitystore.ListGroupsInput, _ ...func(*identitystore.Options)) (*identitystore.ListGroupsOutput, error) {
	p := pageFor(s.groups, aws.ToString(in.NextToken))
	return &identitystore.ListGroupsOutput{Groups: p.items, NextToken: nextToken(p.next)}, nil
}

type stubOrganizations struct {
	roots []orgtypes.Root
	// children maps a parent ID to the pages of OUs beneath it.
	children map[string][]page[orgtypes.OrganizationalUnit]
	rootsErr error
}

func (s *stubOrganizations) ListRoots(context.Context, *organizations.ListRootsInput, ...func(*organizations.Options)) (*organizations.ListRootsOutput, error) {
	if s.rootsErr != nil {
		return nil, s.rootsErr
	}
	return &organizations.ListRootsOutput{Roots: s.roots}, nil
}

func (s *stubOrganizations) ListOrganizationalUnitsForParent(_ context.Context, in *organizations.ListOrganizationalUnitsForParentInput, _ ...func(*organizations.Options)) (*organizations.ListOrganizationalUnitsForParentOutput, error) {
	pages := s.children[aws.ToString(in.ParentId)]
	if len(pages) == 0 {
		return &organizations.ListOrganizationalUnitsForParentOutput{}, nil
	}
	p := pageFor(pages, aws.ToString(in.NextToken))
	return &organizations.ListOrganizationalUnitsForParentOutput{
		OrganizationalUnits: p.items,
		NextToken:           nextToken(p.next),
	}, nil
}

type stubServiceCatalog struct {
	products    []sctypes.ProductViewSummary
	artifacts   []sctypes.ProvisioningArtifactDetail
	err         error
	artifactErr error
}

func (s *stubServiceCatalog) SearchProducts(context.Context, *servicecatalog.SearchProductsInput, ...func(*servicecatalog.Options)) (*servicecatalog.SearchProductsOutput, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &servicecatalog.SearchProductsOutput{ProductViewSummaries: s.products}, nil
}

func (s *stubServiceCatalog) ListProvisioningArtifacts(context.Context, *servicecatalog.ListProvisioningArtifactsInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListProvisioningArtifactsOutput, error) {
	if s.artifactErr != nil {
		return nil, s.artifactErr
	}
	return &servicecatalog.ListProvisioningArtifactsOutput{ProvisioningArtifactDetails: s.artifacts}, nil
}

func pageFor[T any](pages []page[T], token string) page[T] {
	if len(pages) == 0 {
		return page[T]{}
	}
	if token == "" {
		return pages[0]
	}
	for i, p := range pages {
		if p.next == token && i+1 < len(pages) {
			return pages[i+1]
		}
	}
	return page[T]{}
}

// workingDeps returns a Deps that describes a small, healthy organization.
func workingDeps() Deps {
	return Deps{
		SSOAdmin: &stubSSOAdmin{
			instances: []ssotypes.InstanceMetadata{{
				InstanceArn:     aws.String("arn:aws:sso:::instance/ssoins-123"),
				IdentityStoreId: aws.String("d-1234567890"),
			}},
			permissionSets: []page[string]{
				{items: []string{"arn:ps/admin"}, next: "p2"},
				{items: []string{"arn:ps/readonly"}},
			},
			names: map[string]string{
				"arn:ps/admin":    "AdministratorAccess",
				"arn:ps/readonly": "ReadOnlyAccess",
			},
		},
		IdentityStore: &stubIdentityStore{
			users: []page[idtypes.User]{{items: []idtypes.User{
				{UserName: aws.String("taro"), UserId: aws.String("u-1")},
			}}},
			groups: []page[idtypes.Group]{{items: []idtypes.Group{
				{DisplayName: aws.String("Developers"), GroupId: aws.String("g-1")},
			}}},
		},
		Organizations: &stubOrganizations{
			roots: []orgtypes.Root{{Id: aws.String("r-root")}},
			children: map[string][]page[orgtypes.OrganizationalUnit]{
				"r-root": {{items: []orgtypes.OrganizationalUnit{
					{Id: aws.String("ou-abcd-12345678"), Name: aws.String("Sandbox")},
				}}},
				"ou-abcd-12345678": {{items: []orgtypes.OrganizationalUnit{
					{Id: aws.String("ou-abcd-87654321"), Name: aws.String("Nested")},
				}}},
			},
		},
		ServiceCatalog: &stubServiceCatalog{
			products: []sctypes.ProductViewSummary{
				{Name: aws.String("Some Other Product"), ProductId: aws.String("prod-other")},
				{Name: aws.String(AccountFactoryProductName), ProductId: aws.String("prod-af")},
			},
			artifacts: []sctypes.ProvisioningArtifactDetail{
				{Id: aws.String("pa-old"), Active: aws.Bool(true)},
				{Id: aws.String("pa-retired"), Active: aws.Bool(false)},
				{Id: aws.String("pa-current"), Active: aws.Bool(true)},
			},
		},
	}
}
