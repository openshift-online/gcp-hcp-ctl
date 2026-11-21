package application

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Step struct {
	LastFailedOperation     string    `json:"lastFailedOperation,omitempty"`
	BeforePolicyFingerprint string    `json:"beforePolicyFingerprint,omitempty"`
	AfterPolicyFingerprint  string    `json:"afterPolicyFingerprint,omitempty"`
	PolicyEtag              string    `json:"policyEtag,omitempty"`
	DeleteOperation         string    `json:"deleteOperation,omitempty"`
	State                   string    `json:"state"`
	IntentAt                time.Time `json:"intentAt,omitempty"`
	Resource                *Resource `json:"resource,omitempty"`
	Added                   bool      `json:"added,omitempty"`
}
type Submission struct {
	PreflightAbsent bool            `json:"preflightAbsent,omitempty"`
	State           string          `json:"state"`
	Namespace       string          `json:"namespace,omitempty"`
	Name            string          `json:"name,omitempty"`
	UID             string          `json:"uid,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	IntentAt        time.Time       `json:"intentAt,omitempty"`
}
type Journal struct {
	Schema                  int              `json:"schema"`
	OperationID             string           `json:"operationID"`
	Plan                    Plan             `json:"plan"`
	Fingerprint             string           `json:"fingerprint"`
	Phase                   string           `json:"phase"`
	Steps                   map[string]*Step `json:"steps"`
	Cluster                 Submission       `json:"cluster"`
	VerifiedAt              *time.Time       `json:"verifiedAt,omitempty"`
	NetworkFingerprint      string           `json:"networkFingerprint,omitempty"`
	NetworkExternal         bool             `json:"networkExternal,omitempty"`
	NetworkSpec             json.RawMessage  `json:"networkSpec,omitempty"`
	InstallationFingerprint string           `json:"installationFingerprint,omitempty"`
	InstallationPayload     json.RawMessage  `json:"installationPayload,omitempty"`
}

func newJournal(p *Plan) (*Journal, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("cannot generate operation identity")
	}
	return &Journal{Schema: 1, OperationID: hex.EncodeToString(b), Plan: *p, Fingerprint: p.Fingerprint(), Phase: "Provisioning", Steps: map[string]*Step{}, Cluster: Submission{State: "None"}}, nil
}
func (j *Journal) Owner() string {
	sum := sha256.Sum256([]byte(j.Plan.Identity.ProjectNumber + "/" + j.Plan.Identity.InfraID + "/" + j.Plan.Target.Reference.Digest))
	return "gcphcpctl/v1/" + j.OperationID + "/" + hex.EncodeToString(sum[:])
}
func (j *Journal) validate(p *Plan) error {
	if j.Schema != 1 || len(j.OperationID) != 32 || j.Steps == nil || j.Cluster.State == "" {
		return fmt.Errorf("unsupported or incomplete operation journal")
	}
	if _, err := hex.DecodeString(j.OperationID); err != nil {
		return fmt.Errorf("invalid operation identity")
	}
	rebuilt, err := Build(&j.Plan.Target, j.Plan.Identity)
	if err != nil || rebuilt.Fingerprint() != j.Fingerprint || j.Plan.Fingerprint() != j.Fingerprint {
		return fmt.Errorf("journal plan integrity mismatch")
	}
	if p != nil && p.Fingerprint() != j.Fingerprint {
		return fmt.Errorf("operation binding changed; refusing journal reuse")
	}
	return nil
}

type JournalStore interface{ Save(*Journal) error }
type FileJournal struct {
	Path    string
	lock    *os.File
	Journal *Journal
}

var ErrOrphanJournal = errors.New("empty protected journal has no established operation; use an explicit reviewed resume")

func privateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("operation file must be a private regular file")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("operation file ownership mismatch")
	}
	return nil
}

// Load is read-only: verify/preview do not create lock files or journals.
func Load(path string) (*Journal, error) {
	if err := privateFile(path); err != nil {
		return nil, fmt.Errorf("cannot read a protected operation journal")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read operation journal")
	}
	if len(data) == 0 {
		return nil, ErrOrphanJournal
	}
	var j Journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("invalid operation journal JSON")
	}
	if err := j.validate(nil); err != nil {
		return nil, err
	}
	return &j, nil
}

func Open(path string, p *Plan, resume bool) (*FileJournal, error) {
	return openJournal(path, p, resume, false)
}

func openJournal(path string, p *Plan, resume, linked bool) (*FileJournal, error) {
	if path == "" {
		return nil, fmt.Errorf("--state-file is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid state path")
	}
	dir := filepath.Dir(abs)
	for parent := dir; parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		i, err := os.Lstat(parent)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || i.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("journal path must not contain symlinks")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("cannot prepare private journal directory")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("journal directory must be private (0700), not a symlink")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("journal directory ownership mismatch")
	}
	// Refuse symlink ancestors as well as final file/lock symlinks.
	for parent := dir; parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		i, err := os.Lstat(parent)
		if err != nil || i.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("journal path must not contain symlinks")
		}
	}
	lock, err := os.OpenFile(abs+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot acquire protected journal lock")
	}
	if err := privateFile(abs + ".lock"); err != nil {
		lock.Close()
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another operation holds this journal lock")
	}
	f := &FileJournal{Path: abs, lock: lock}
	fail := func(err error) (*FileJournal, error) { f.Close(); return nil, err }
	if !linked {
		if _, err := os.Lstat(UpdateStatePath(abs)); err == nil || !os.IsNotExist(err) {
			return fail(fmt.Errorf("installation has a linked IAM update journal; use update-aware commands"))
		}
	}
	if resume {
		j, err := Load(abs)
		if errors.Is(err, ErrOrphanJournal) {
			j, err = newJournal(p)
			if err == nil {
				f.Journal = j
				err = f.Save(j)
			}
		}
		if err != nil {
			return fail(err)
		}
		if err := j.validate(p); err != nil {
			return fail(err)
		}
		f.Journal = j
		return f, nil
	}
	file, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return fail(fmt.Errorf("state file exists or cannot be created; existing operations require --resume"))
	}
	file.Close()
	j, err := newJournal(p)
	if err != nil {
		return fail(err)
	}
	f.Journal = j
	if err := f.Save(j); err != nil {
		return fail(err)
	}
	return f, nil
}
func (f *FileJournal) Save(j *Journal) error {
	if f.lock == nil {
		return fmt.Errorf("journal lock is not held")
	}
	if err := privateFile(f.Path); err != nil {
		return fmt.Errorf("journal file protection changed")
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode operation journal")
	}
	return atomicPrivateSave(f.Path, data)
}

func atomicPrivateSave(path string, data []byte) error {
	if err := privateFile(path); err != nil {
		return fmt.Errorf("journal file protection changed")
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".gcphcpctl-state-")
	if err != nil {
		return fmt.Errorf("cannot prepare journal update")
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(append(data, '\n')); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil || closeErr != nil {
		return fmt.Errorf("cannot persist journal update")
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("cannot replace journal atomically")
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("cannot sync journal directory")
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("cannot sync journal directory")
	}
	return nil
}
func (f *FileJournal) Close() error {
	if f.lock == nil {
		return nil
	}
	_ = syscall.Flock(int(f.lock.Fd()), syscall.LOCK_UN)
	err := f.lock.Close()
	f.lock = nil
	return err
}
