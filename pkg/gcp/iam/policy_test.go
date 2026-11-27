package iam

import (
	"testing"

	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v1"
)

func TestRemoveProjectPolicyMemberDropsEmptyBinding(t *testing.T) {
	policy := &cloudresourcemanager.Policy{
		Bindings: []*cloudresourcemanager.Binding{
			{Role: "roles/viewer", Members: []string{"user:a@example.com"}},
			{Role: "roles/dns.admin", Members: []string{"serviceAccount:sa@example.com", "user:b@example.com"}},
		},
	}

	if !RemoveProjectPolicyMember(policy, "roles/viewer", "user:a@example.com") {
		t.Fatal("expected the member to be removed")
	}
	if len(policy.Bindings) != 1 || policy.Bindings[0].Role != "roles/dns.admin" {
		t.Fatalf("a binding that lost its last member must be dropped, got %+v", policy.Bindings)
	}
	if got := len(policy.Bindings[0].Members); got != 2 {
		t.Fatalf("unrelated binding changed, got %d members", got)
	}

	// Removing one of several members keeps the binding.
	if !RemoveProjectPolicyMember(policy, "roles/dns.admin", "serviceAccount:sa@example.com") {
		t.Fatal("expected the member to be removed")
	}
	if len(policy.Bindings) != 1 {
		t.Fatalf("binding with a remaining member must stay, got %+v", policy.Bindings)
	}
	if got := policy.Bindings[0].Members; len(got) != 1 || got[0] != "user:b@example.com" {
		t.Fatalf("unexpected remaining members %v", got)
	}

	// The last member takes the binding with it.
	if !RemoveProjectPolicyMember(policy, "roles/dns.admin", "user:b@example.com") {
		t.Fatal("expected the member to be removed")
	}
	if len(policy.Bindings) != 0 {
		t.Fatalf("expected no bindings left, got %+v", policy.Bindings)
	}
}
