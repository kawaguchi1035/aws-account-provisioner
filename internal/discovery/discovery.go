// Package discovery reads the AWS-side state a provisioning run depends on:
// the Account Factory product, the IAM Identity Center instance, and the
// permission sets, principals and organizational units it will reference.
//
// Everything is fetched once, up front, and held in a Catalog. Permission set
// names in particular are expensive to resolve — ListPermissionSets returns
// ARNs only, so each name costs a DescribePermissionSet call — and resolving
// them lazily would repeat that cost on every lookup.
package discovery

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/servicecatalog"
	sctypes "github.com/aws/aws-sdk-go-v2/service/servicecatalog/types"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
)

// AccountFactoryProductName is the Service Catalog product Control Tower
// publishes for account creation.
const AccountFactoryProductName = "AWS Control Tower Account Factory"

// SSOAdminAPI is the slice of AWS SSO Admin this package uses.
type SSOAdminAPI interface {
	ListInstances(ctx context.Context, params *ssoadmin.ListInstancesInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.ListInstancesOutput, error)
	ListPermissionSets(ctx context.Context, params *ssoadmin.ListPermissionSetsInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.ListPermissionSetsOutput, error)
	DescribePermissionSet(ctx context.Context, params *ssoadmin.DescribePermissionSetInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.DescribePermissionSetOutput, error)
}

// IdentityStoreAPI is the slice of Identity Store this package uses.
type IdentityStoreAPI interface {
	ListUsers(ctx context.Context, params *identitystore.ListUsersInput, optFns ...func(*identitystore.Options)) (*identitystore.ListUsersOutput, error)
	ListGroups(ctx context.Context, params *identitystore.ListGroupsInput, optFns ...func(*identitystore.Options)) (*identitystore.ListGroupsOutput, error)
}

// OrganizationsAPI is the slice of Organizations this package uses.
type OrganizationsAPI interface {
	ListRoots(ctx context.Context, params *organizations.ListRootsInput, optFns ...func(*organizations.Options)) (*organizations.ListRootsOutput, error)
	ListOrganizationalUnitsForParent(ctx context.Context, params *organizations.ListOrganizationalUnitsForParentInput, optFns ...func(*organizations.Options)) (*organizations.ListOrganizationalUnitsForParentOutput, error)
}

// ServiceCatalogAPI is the slice of Service Catalog this package uses.
type ServiceCatalogAPI interface {
	SearchProducts(ctx context.Context, params *servicecatalog.SearchProductsInput, optFns ...func(*servicecatalog.Options)) (*servicecatalog.SearchProductsOutput, error)
	ListProvisioningArtifacts(ctx context.Context, params *servicecatalog.ListProvisioningArtifactsInput, optFns ...func(*servicecatalog.Options)) (*servicecatalog.ListProvisioningArtifactsOutput, error)
}

// Deps holds the AWS clients Load needs.
type Deps struct {
	SSOAdmin       SSOAdminAPI
	IdentityStore  IdentityStoreAPI
	Organizations  OrganizationsAPI
	ServiceCatalog ServiceCatalogAPI
}

// NewDeps builds Deps from a resolved AWS configuration.
func NewDeps(cfg aws.Config) Deps {
	return Deps{
		SSOAdmin:       ssoadmin.NewFromConfig(cfg),
		IdentityStore:  identitystore.NewFromConfig(cfg),
		Organizations:  organizations.NewFromConfig(cfg),
		ServiceCatalog: servicecatalog.NewFromConfig(cfg),
	}
}

// Catalog is the AWS-side state, fetched once and queried many times.
type Catalog struct {
	InstanceARN             string
	IdentityStoreID         string
	AccountFactoryProductID string

	// AccountFactoryArtifactID is the provisioning artifact (product version)
	// used to launch new accounts.
	AccountFactoryArtifactID string

	permissionSets map[string]string // name -> ARN
	users          map[string]string // user name -> ID
	groups         map[string]string // display name -> ID
	ous            map[string]string // OU ID -> name
}

// Load fetches everything the run depends on.
func Load(ctx context.Context, deps Deps) (*Catalog, error) {
	instanceARN, identityStoreID, err := findIdentityCenterInstance(ctx, deps.SSOAdmin)
	if err != nil {
		return nil, err
	}

	productID, err := findAccountFactoryProduct(ctx, deps.ServiceCatalog)
	if err != nil {
		return nil, err
	}

	artifactID, err := findActiveArtifact(ctx, deps.ServiceCatalog, productID)
	if err != nil {
		return nil, err
	}

	permissionSets, err := loadPermissionSets(ctx, deps.SSOAdmin, instanceARN)
	if err != nil {
		return nil, err
	}

	users, err := loadUsers(ctx, deps.IdentityStore, identityStoreID)
	if err != nil {
		return nil, err
	}

	groups, err := loadGroups(ctx, deps.IdentityStore, identityStoreID)
	if err != nil {
		return nil, err
	}

	ous, err := loadOUs(ctx, deps.Organizations)
	if err != nil {
		return nil, err
	}

	return &Catalog{
		InstanceARN:              instanceARN,
		IdentityStoreID:          identityStoreID,
		AccountFactoryProductID:  productID,
		AccountFactoryArtifactID: artifactID,
		permissionSets:           permissionSets,
		users:                    users,
		groups:                   groups,
		ous:                      ous,
	}, nil
}

func findIdentityCenterInstance(ctx context.Context, api SSOAdminAPI) (instanceARN, identityStoreID string, err error) {
	out, err := api.ListInstances(ctx, &ssoadmin.ListInstancesInput{})
	if err != nil {
		return "", "", fmt.Errorf("list IAM Identity Center instances: %w", err)
	}
	switch len(out.Instances) {
	case 0:
		return "", "", fmt.Errorf("no IAM Identity Center instance found; enable it in this account and region")
	case 1:
		i := out.Instances[0]
		return aws.ToString(i.InstanceArn), aws.ToString(i.IdentityStoreId), nil
	default:
		return "", "", fmt.Errorf("found %d IAM Identity Center instances, expected exactly 1", len(out.Instances))
	}
}

func findAccountFactoryProduct(ctx context.Context, api ServiceCatalogAPI) (string, error) {
	out, err := api.SearchProducts(ctx, &servicecatalog.SearchProductsInput{
		Filters: map[string][]string{
			string(sctypes.ProductViewFilterByFullTextSearch): {AccountFactoryProductName},
		},
	})
	if err != nil {
		return "", fmt.Errorf("search for the Account Factory product: %w", err)
	}
	for _, p := range out.ProductViewSummaries {
		if aws.ToString(p.Name) == AccountFactoryProductName {
			return aws.ToString(p.ProductId), nil
		}
	}
	return "", fmt.Errorf("product %q not found in Service Catalog; check that Control Tower is enabled in this region "+
		"and that the caller has access to the Account Factory portfolio", AccountFactoryProductName)
}

// findActiveArtifact picks the most recent active version of the product.
// Account Factory is updated by Control Tower over time, and launching an
// inactive version fails, so the newest active one is always the right choice.
func findActiveArtifact(ctx context.Context, api ServiceCatalogAPI, productID string) (string, error) {
	out, err := api.ListProvisioningArtifacts(ctx, &servicecatalog.ListProvisioningArtifactsInput{
		ProductId: aws.String(productID),
	})
	if err != nil {
		return "", fmt.Errorf("list provisioning artifacts: %w", err)
	}
	for i := len(out.ProvisioningArtifactDetails) - 1; i >= 0; i-- {
		a := out.ProvisioningArtifactDetails[i]
		if aws.ToBool(a.Active) && a.Id != nil {
			return aws.ToString(a.Id), nil
		}
	}
	return "", fmt.Errorf("no active provisioning artifact found for the Account Factory product")
}

func loadPermissionSets(ctx context.Context, api SSOAdminAPI, instanceARN string) (map[string]string, error) {
	byName := map[string]string{}

	var next *string
	for {
		out, err := api.ListPermissionSets(ctx, &ssoadmin.ListPermissionSetsInput{
			InstanceArn: aws.String(instanceARN),
			NextToken:   next,
		})
		if err != nil {
			return nil, fmt.Errorf("list permission sets: %w", err)
		}
		for _, arn := range out.PermissionSets {
			desc, err := api.DescribePermissionSet(ctx, &ssoadmin.DescribePermissionSetInput{
				InstanceArn:      aws.String(instanceARN),
				PermissionSetArn: aws.String(arn),
			})
			if err != nil {
				return nil, fmt.Errorf("describe permission set %s: %w", arn, err)
			}
			if desc.PermissionSet == nil {
				continue
			}
			byName[aws.ToString(desc.PermissionSet.Name)] = arn
		}
		if next = out.NextToken; next == nil {
			return byName, nil
		}
	}
}

func loadUsers(ctx context.Context, api IdentityStoreAPI, identityStoreID string) (map[string]string, error) {
	byName := map[string]string{}

	var next *string
	for {
		out, err := api.ListUsers(ctx, &identitystore.ListUsersInput{
			IdentityStoreId: aws.String(identityStoreID),
			NextToken:       next,
		})
		if err != nil {
			return nil, fmt.Errorf("list Identity Center users: %w", err)
		}
		for _, u := range out.Users {
			byName[aws.ToString(u.UserName)] = aws.ToString(u.UserId)
		}
		if next = out.NextToken; next == nil {
			return byName, nil
		}
	}
}

func loadGroups(ctx context.Context, api IdentityStoreAPI, identityStoreID string) (map[string]string, error) {
	byName := map[string]string{}

	var next *string
	for {
		out, err := api.ListGroups(ctx, &identitystore.ListGroupsInput{
			IdentityStoreId: aws.String(identityStoreID),
			NextToken:       next,
		})
		if err != nil {
			return nil, fmt.Errorf("list Identity Center groups: %w", err)
		}
		for _, g := range out.Groups {
			byName[aws.ToString(g.DisplayName)] = aws.ToString(g.GroupId)
		}
		if next = out.NextToken; next == nil {
			return byName, nil
		}
	}
}

// loadOUs walks the whole organization, so a nested OU can be referenced by its
// identifier without the caller having to spell out the path to it.
func loadOUs(ctx context.Context, api OrganizationsAPI) (map[string]string, error) {
	roots, err := api.ListRoots(ctx, &organizations.ListRootsInput{})
	if err != nil {
		return nil, fmt.Errorf("list organization roots: %w", err)
	}

	byID := map[string]string{}
	for _, root := range roots.Roots {
		if err := walkOUs(ctx, api, aws.ToString(root.Id), byID); err != nil {
			return nil, err
		}
	}
	return byID, nil
}

func walkOUs(ctx context.Context, api OrganizationsAPI, parentID string, byID map[string]string) error {
	var next *string
	for {
		out, err := api.ListOrganizationalUnitsForParent(ctx, &organizations.ListOrganizationalUnitsForParentInput{
			ParentId:  aws.String(parentID),
			NextToken: next,
		})
		if err != nil {
			return fmt.Errorf("list organizational units under %s: %w", parentID, err)
		}
		for _, ou := range out.OrganizationalUnits {
			id := aws.ToString(ou.Id)
			if _, seen := byID[id]; seen {
				continue
			}
			byID[id] = aws.ToString(ou.Name)
			if err := walkOUs(ctx, api, id, byID); err != nil {
				return err
			}
		}
		if next = out.NextToken; next == nil {
			return nil
		}
	}
}
