package awsauth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type stubSTS struct {
	account string
	err     error
}

func (s stubSTS) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := &sts.GetCallerIdentityOutput{}
	if s.account != "" {
		out.Account = aws.String(s.account)
	}
	return out, nil
}

func TestRoleARN(t *testing.T) {
	got := RoleARN("123456789012", "OrganizationAccountAccessRole")
	want := "arn:aws:iam::123456789012:role/OrganizationAccountAccessRole"
	if got != want {
		t.Errorf("RoleARN() = %q, want %q", got, want)
	}
}

func TestCheckAccount(t *testing.T) {
	tests := []struct {
		name    string
		got     string
		want    string
		wantErr bool
	}{
		{name: "matching account", got: "123456789012", want: "123456789012"},
		{name: "different account", got: "999999999999", want: "123456789012", wantErr: true},
		{name: "empty account", got: "", want: "123456789012", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkAccount(tt.got, tt.want)
			if tt.wantErr != (err != nil) {
				t.Fatalf("checkAccount(%q, %q) error = %v, wantErr %v", tt.got, tt.want, err, tt.wantErr)
			}
		})
	}
}

func TestCheckAccountErrorNamesBothAccounts(t *testing.T) {
	err := checkAccount("999999999999", "123456789012")
	if err == nil {
		t.Fatal("checkAccount() = nil error, want error")
	}
	for _, want := range []string{"999999999999", "123456789012"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestVerify(t *testing.T) {
	tests := []struct {
		name    string
		api     stubSTS
		wantErr bool
	}{
		{name: "identity matches", api: stubSTS{account: "123456789012"}},
		{name: "identity differs", api: stubSTS{account: "999999999999"}, wantErr: true},
		{name: "sts call fails", api: stubSTS{err: errors.New("expired token")}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verify(context.Background(), tt.api, "123456789012")
			if tt.wantErr != (err != nil) {
				t.Fatalf("verify() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
