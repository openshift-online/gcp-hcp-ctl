package application

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
)

// Prepare performs read-only catalog and actual worker identity resolution.
func Prepare(ctx context.Context, c Catalog, cloud Cloud, project, infra, issuer, version, group string) (*Plan, error) {
	if c == nil || cloud == nil {
		return nil, fmt.Errorf("authenticated catalog and cloud readers are required")
	}
	realm, ok := c.(interface{ EndpointIdentity() (string, error) })
	if !ok {
		return nil, fmt.Errorf("catalog API endpoint binding is required")
	}
	endpoint, err := realm.EndpointIdentity()
	if err != nil {
		return nil, err
	}
	target, err := Resolve(ctx, c, version, group)
	if err != nil {
		return nil, err
	}
	number, err := cloud.ProjectNumber(ctx)
	if err != nil {
		return nil, cloudError(err)
	}
	email := number + "-compute@developer.gserviceaccount.com"
	worker, err := cloud.Get(ctx, Resource{Kind: "ServiceAccount", Name: "projects/" + project + "/serviceAccounts/" + email})
	if err != nil {
		return nil, cloudError(err)
	}
	if worker.Disabled || worker.Deleted || worker.UID == "" {
		return nil, fmt.Errorf("default Compute worker service account must exist and be enabled")
	}
	return Build(target, Identity{APIEndpoint: endpoint, Project: project, ProjectNumber: number, InfraID: infra, Issuer: issuer, WorkerEmail: email, WorkerUID: worker.UID})
}

type Session struct {
	File    *FileJournal
	Engine  *Engine
	Catalog Catalog
}

func Start(ctx context.Context, c Catalog, cloud Cloud, p *Plan, path, expected string, resume bool) (*Session, error) {
	if err := CheckRealm(ctx, c, p); err != nil {
		return nil, err
	}
	if expected == "" || expected != p.Fingerprint() {
		return nil, fmt.Errorf("--expect-plan must match the fresh preview fingerprint")
	}
	if err := p.Target.Eligible(); err != nil {
		return nil, err
	}
	if err := Recheck(ctx, c, &p.Target); err != nil {
		return nil, err
	}
	file, err := Open(path, p, resume)
	if err != nil {
		return nil, err
	}
	e := &Engine{Cloud: cloud, Store: file, Recheck: func(ctx context.Context) error { return Recheck(ctx, c, &p.Target) }}
	return &Session{File: file, Engine: e, Catalog: c}, nil
}

// CheckRealm binds read-only resolution and teardown to the realm that supplied
// the reviewed contract. No availability requirement is imposed on cleanup.
func CheckRealm(ctx context.Context, c Catalog, p *Plan) error {
	realm, ok := c.(interface{ EndpointIdentity() (string, error) })
	if !ok {
		return fmt.Errorf("catalog API realm is required")
	}
	endpoint, err := realm.EndpointIdentity()
	if err != nil {
		return err
	}
	if endpoint != p.Identity.APIEndpoint {
		return fmt.Errorf("API realm differs from the journaled installation")
	}
	v, err := c.Versions().Get(ctx, p.Target.Version)
	if err != nil {
		return err
	}
	if string(v.UID) != p.Target.VersionUID || v.Name != p.Target.Version {
		return fmt.Errorf("catalog Version identity differs from the journaled API realm")
	}
	return nil
}
func (s *Session) Close() error                    { return s.File.Close() }
func (s *Session) Apply(ctx context.Context) error { return s.Engine.Apply(ctx, s.File.Journal) }
func (s *Session) Verify(ctx context.Context) error {
	if err := Recheck(ctx, s.Catalog, &s.File.Journal.Plan.Target); err != nil {
		return err
	}
	if err := s.Engine.Verify(ctx, &s.File.Journal.Plan, s.File.Journal); err != nil {
		return err
	}
	now := time.Now().UTC()
	s.File.Journal.VerifiedAt = &now
	return s.File.Save(s.File.Journal)
}
func (s *Session) Application() (v1.ClusterIAMSpec, error) {
	j := s.File.Journal
	if j.Phase != "IAMVerified" || j.VerifiedAt == nil {
		return v1.ClusterIAMSpec{}, fmt.Errorf("IAM readback has not completed")
	}
	return v1.ClusterIAMSpec{WorkerServiceAccount: j.Plan.Identity.WorkerEmail, TemplateApplications: []v1.WIFConfigTemplateReference{j.Plan.Target.Reference}}, nil
}
