package app

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/fsx"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// firstRunIdentity is a protected restart locator, not a plan. Its lock
// objects and bootstrap OID must still be checked against the remote before
// the first node can adopt this generation or accept another claim.
type firstRunIdentity struct {
	Run            string                                  `json:"run"`
	Endpoint       string                                  `json:"endpoint"`
	Listen         string                                  `json:"listen"`
	CertPin        string                                  `json:"certPin"`
	CertificatePEM string                                  `json:"certificatePem"`
	PrivateKeyPEM  string                                  `json:"privateKeyPem"`
	PreparedLocks  map[string]release.PreparedLockIdentity `json:"preparedLocks,omitempty"`
	DeferredTags   map[string]firstRunDeferredTag          `json:"deferredTags,omitempty"`
	Generation     string                                  `json:"generation,omitempty"`
	BootstrapOID   string                                  `json:"bootstrapOid,omitempty"`
	LedgerRootOID  string                                  `json:"ledgerRootOid,omitempty"`
}

// firstRunDeferredTag is written in two steps: the task records its expected
// tag name before signed local finality, then finalize writes the exact
// target before the first tag ref is created. Object is an additional exact
// observation made immediately after creation; a crash before that write is
// reconciled from the protected target intent and the live tag.
type firstRunDeferredTag struct {
	Tag           string `json:"tag"`
	Target        string `json:"target,omitempty"`
	Object        string `json:"object,omitempty"`
	ReceiptObject string `json:"receiptObject,omitempty"`
	ReceiptCommit string `json:"receiptCommit,omitempty"`
	BranchHead    string `json:"branchHead,omitempty"`
	Branch        string `json:"branch,omitempty"`
}

type firstRunJournal struct {
	dir  string
	run  string
	lock *os.File
	gate *os.File
	mu   sync.Mutex
}

// openFirstRunJournal holds an operating-system file lock for this run's
// lifetime. Its stable checkout-level lock serializes distributed first-node
// runs in one canonical Git directory, including sweeps; other checkouts may
// run independently. A stale process ID alone never grants authority.
func openFirstRunJournal(gitDir, run string) (*firstRunJournal, error) {
	if !validJournalRun(run) || gitDir == firstRunJournalAbsentIdentityText {
		return nil, fmt.Errorf(firstRunJournalInvalidPrivateIdentityErrorText)
	}
	base := filepath.Join(gitDir, firstRunJournalApplicationDirectoryName, firstRunJournalRunsDirectoryName)
	if err := os.MkdirAll(base, firstRunJournalPrivateDirectoryMode); err != nil {
		return nil, err
	}
	if err := requirePrivateDirectory(filepath.Join(gitDir, firstRunJournalApplicationDirectoryName)); err != nil {
		return nil, err
	}
	if err := requirePrivateDirectory(base); err != nil {
		return nil, err
	}
	gatePath := filepath.Join(base, firstRunJournalCheckoutLockFileName)
	gate, err := openPrivateRunLock(gatePath)
	if err != nil {
		return nil, fmt.Errorf(firstRunJournalCheckoutAlreadyOwnedErrorFormat, err)
	}
	keepGate := firstRunJournalGateUnretainedState
	defer func() {
		if !keepGate {
			_ = gate.Close()
		}
	}()
	activePath := filepath.Join(base, firstRunJournalActiveLocatorFileName)
	active, err := readActiveRun(activePath)
	if err != nil {
		return nil, err
	}
	if active != firstRunJournalAbsentIdentityText && active != run {
		return nil, fmt.Errorf(firstRunJournalPriorRunUnreconciledErrorFormat, active)
	}
	dir := filepath.Join(base, run)
	if err := os.Mkdir(dir, firstRunJournalPrivateDirectoryMode); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := requirePrivateDirectory(dir); err != nil {
		return nil, err
	}
	// A file fsync cannot make newly created ancestor directory entries
	// durable. Publish every link before a remote lock can be attempted.
	for _, parent := range []string{gitDir, filepath.Join(gitDir, firstRunJournalApplicationDirectoryName), base, dir} {
		if err := syncRunDirectory(parent); err != nil {
			return nil, fmt.Errorf(firstRunJournalDirectoryNotDurableErrorFormat, err)
		}
	}
	lock, err := openPrivateRunLock(filepath.Join(dir, firstRunJournalOwnerLockFileName))
	if err != nil {
		return nil, fmt.Errorf(firstRunJournalRunAlreadyOwnedErrorFormat, run, err)
	}
	if active == firstRunJournalAbsentIdentityText {
		if err := fsx.WriteFileAtomic(activePath, []byte(run), firstRunJournalPrivateFileMode); err != nil {
			_ = lock.Close()
			return nil, err
		}
		if err := syncRunDirectory(base); err != nil {
			_ = lock.Close()
			return nil, err
		}
	}
	keepGate = firstRunJournalGateRetainedState
	return &firstRunJournal{dir: dir, run: run, lock: lock, gate: gate}, nil
}

func openPrivateRunLock(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != firstRunJournalNoPermissionBits ||
		info.Mode().Perm()&firstRunJournalOtherUsersPermissionMask != firstRunJournalNoPermissionBits) {
		return nil, fmt.Errorf(firstRunJournalUnsafeLockFileErrorText)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, firstRunJournalPrivateFileMode)
	if err != nil {
		return nil, err
	}
	if err := lockRunFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf(firstRunJournalLockMetadataUnavailableErrorFormat, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&firstRunJournalOtherUsersPermissionMask != firstRunJournalNoPermissionBits {
		_ = file.Close()
		return nil, fmt.Errorf(firstRunJournalUnsafeLockFileErrorText)
	}
	return file, nil
}

func readActiveRun(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return firstRunJournalAbsentIdentityText, nil
	}
	if err != nil {
		return firstRunJournalAbsentIdentityText, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&firstRunJournalOtherUsersPermissionMask != firstRunJournalNoPermissionBits || info.Size() != firstRunJournalRunIDHexCharacters {
		return firstRunJournalAbsentIdentityText, fmt.Errorf(firstRunJournalUnsafeActiveLocatorErrorText)
	}
	file, err := os.Open(path)
	if err != nil {
		return firstRunJournalAbsentIdentityText, err
	}
	defer file.Close()
	bytes, err := io.ReadAll(io.LimitReader(file, firstRunJournalActiveLocatorReadBytes))
	if err != nil {
		return firstRunJournalAbsentIdentityText, fmt.Errorf(firstRunJournalActiveLocatorUnreadableErrorFormat, err)
	}
	if len(bytes) != firstRunJournalRunIDHexCharacters || !validJournalRun(string(bytes)) {
		return firstRunJournalAbsentIdentityText, fmt.Errorf(firstRunJournalInvalidActiveLocatorErrorText)
	}
	return string(bytes), nil
}

func validJournalRun(run string) bool {
	if len(run) != firstRunJournalRunIDHexCharacters {
		return firstRunJournalInvalidIdentityState
	}
	decoded, err := hex.DecodeString(run)
	return err == nil && hex.EncodeToString(decoded) == run
}

func requirePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != firstRunJournalNoPermissionBits || info.Mode().Perm()&firstRunJournalOtherUsersPermissionMask != firstRunJournalNoPermissionBits {
		return fmt.Errorf(firstRunJournalDirectoryNotPrivateErrorFormat, path)
	}
	return nil
}

func (j *firstRunJournal) write(identity firstRunIdentity) error {
	if j == nil || j.lock == nil || identity.Run != j.run {
		return fmt.Errorf(firstRunJournalIdentityMismatchErrorText)
	}
	if _, err := identity.certificateIntegrity(); err != nil {
		return err
	}
	if err := identity.validate(); err != nil {
		return err
	}
	document, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	if len(document) > firstRunJournalMaximumIdentityBytes {
		return fmt.Errorf(firstRunJournalIdentityLimitErrorText)
	}
	path := filepath.Join(j.dir, firstRunJournalIdentityFileName)
	if err := fsx.WriteFileAtomic(path, document, firstRunJournalPrivateFileMode); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return syncRunDirectory(j.dir)
}

// recordPreparedLock is called while the release lock has a local immutable
// object but before its first remote push. The repository identity disallows
// one fleet member from overwriting another member's recovery locator.
func (j *firstRunJournal) recordPreparedLock(repository string,
	prepared release.PreparedLockIdentity) error {
	identity, err := j.read()
	if err != nil {
		return err
	}
	if identity.PreparedLocks == nil {
		identity.PreparedLocks = make(map[string]release.PreparedLockIdentity)
	}
	if _, exists := identity.PreparedLocks[repository]; exists {
		return fmt.Errorf(firstRunJournalReleaseLockAlreadyPreparedErrorFormat, repository)
	}
	if prepared.Run != j.run || prepared.LocalTag == firstRunJournalAbsentIdentityText || prepared.Object == firstRunJournalAbsentIdentityText || prepared.Remote == firstRunJournalAbsentIdentityText {
		return fmt.Errorf(firstRunJournalReleaseLockIncompleteErrorText)
	}
	identity.PreparedLocks[repository] = prepared
	return j.write(identity)
}

func (j *firstRunJournal) recordBootstrap(oid, generation string) error {
	identity, err := j.read()
	if err != nil {
		return err
	}
	if oid == firstRunJournalAbsentIdentityText || generation == firstRunJournalAbsentIdentityText || identity.BootstrapOID != firstRunJournalAbsentIdentityText || identity.Generation != firstRunJournalAbsentIdentityText {
		return fmt.Errorf(firstRunJournalBootstrapAlreadyPreparedErrorText)
	}
	identity.BootstrapOID = oid
	identity.Generation = generation
	return j.write(identity)
}

func (j *firstRunJournal) recordLedgerRoot(oid string) error {
	identity, err := j.read()
	if err != nil {
		return err
	}
	if oid == firstRunJournalAbsentIdentityText || identity.BootstrapOID == firstRunJournalAbsentIdentityText || identity.LedgerRootOID != firstRunJournalAbsentIdentityText {
		return fmt.Errorf(firstRunJournalLedgerAlreadyPreparedErrorText)
	}
	identity.LedgerRootOID = oid
	return j.write(identity)
}

func (j *firstRunJournal) expectDeferredTag(tag string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	identity, err := j.read()
	if err != nil {
		return err
	}
	if tag == firstRunJournalAbsentIdentityText || identity.LedgerRootOID == firstRunJournalAbsentIdentityText {
		return fmt.Errorf(firstRunJournalDeferredTagLedgerAbsentErrorFormat, tag)
	}
	if identity.DeferredTags == nil {
		identity.DeferredTags = make(map[string]firstRunDeferredTag)
	}
	if _, exists := identity.DeferredTags[tag]; exists {
		return fmt.Errorf(firstRunJournalDeferredTagAlreadyExpectedErrorFormat, tag)
	}
	identity.DeferredTags[tag] = firstRunDeferredTag{Tag: tag}
	return j.write(identity)
}

func (j *firstRunJournal) prepareDeferredTags(intents map[string]firstRunDeferredTag) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	identity, err := j.read()
	if err != nil {
		return err
	}
	if len(identity.DeferredTags) != len(intents) {
		return fmt.Errorf(firstRunJournalDeferredTagIntentSetMismatchErrorText)
	}
	for tag, intent := range intents {
		prior, exists := identity.DeferredTags[tag]
		if !exists || prior.Tag != tag || prior.Target != firstRunJournalAbsentIdentityText || prior.Object != firstRunJournalAbsentIdentityText ||
			intent.Tag != tag || !validJournalObjectOID(intent.Target) ||
			(intent.Object != firstRunJournalAbsentIdentityText && !validJournalObjectOID(intent.Object)) {
			return fmt.Errorf(firstRunJournalDeferredTagIntentInvalidErrorFormat, tag)
		}
		identity.DeferredTags[tag] = intent
	}
	return j.write(identity)
}

func (j *firstRunJournal) recordDeferredTagObject(tag, object string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	identity, err := j.read()
	if err != nil {
		return err
	}
	intent, exists := identity.DeferredTags[tag]
	if !exists || intent.Target == firstRunJournalAbsentIdentityText || !validJournalObjectOID(object) {
		return fmt.Errorf(firstRunJournalDeferredTagObjectSlotAbsentErrorFormat, tag)
	}
	if intent.Object != firstRunJournalAbsentIdentityText {
		if intent.Object != object {
			return fmt.Errorf(firstRunJournalDeferredTagObjectChangedErrorFormat, tag)
		}
		return nil
	}
	intent.Object = object
	identity.DeferredTags[tag] = intent
	return j.write(identity)
}

func (j *firstRunJournal) read() (firstRunIdentity, error) {
	if j == nil {
		return firstRunIdentity{}, fmt.Errorf(firstRunJournalJournalAbsentErrorText)
	}
	path := filepath.Join(j.dir, firstRunJournalIdentityFileName)
	info, err := os.Lstat(path)
	if err != nil {
		return firstRunIdentity{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&firstRunJournalOtherUsersPermissionMask != firstRunJournalNoPermissionBits || info.Size() > firstRunJournalMaximumIdentityBytes {
		return firstRunIdentity{}, fmt.Errorf(firstRunJournalUnsafeIdentityFileErrorText)
	}
	file, err := os.Open(path)
	if err != nil {
		return firstRunIdentity{}, err
	}
	defer file.Close()
	document, err := io.ReadAll(io.LimitReader(file, firstRunJournalMaximumIdentityBytes+firstRunJournalReadProbeBytes))
	if err != nil {
		return firstRunIdentity{}, fmt.Errorf(firstRunJournalIdentityUnreadableErrorFormat, err)
	}
	if len(document) > firstRunJournalMaximumIdentityBytes {
		return firstRunIdentity{}, fmt.Errorf(firstRunJournalIdentityReadLimitErrorText)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var identity firstRunIdentity
	if err := decoder.Decode(&identity); err != nil {
		return firstRunIdentity{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF || identity.Run != j.run {
		return firstRunIdentity{}, fmt.Errorf(firstRunJournalIdentityMalformedErrorText)
	}
	if _, err := identity.certificateIntegrity(); err != nil {
		return firstRunIdentity{}, err
	}
	if err := identity.validate(); err != nil {
		return firstRunIdentity{}, err
	}
	return identity, nil
}

func (i firstRunIdentity) validate() error {
	if !validJournalRun(i.Run) ||
		i.Listen == firstRunJournalAbsentIdentityText ||
		(i.BootstrapOID == firstRunJournalAbsentIdentityText) != (i.Generation == firstRunJournalAbsentIdentityText) ||
		(i.LedgerRootOID != firstRunJournalAbsentIdentityText && i.BootstrapOID == firstRunJournalAbsentIdentityText) ||
		(i.BootstrapOID != firstRunJournalAbsentIdentityText && !validJournalObjectOID(i.BootstrapOID)) ||
		(i.LedgerRootOID != firstRunJournalAbsentIdentityText && !validJournalObjectOID(i.LedgerRootOID)) ||
		(len(i.DeferredTags) != firstRunJournalNoDeferredTagsCount && i.LedgerRootOID == firstRunJournalAbsentIdentityText) {
		return fmt.Errorf(firstRunJournalRunIdentityMalformedErrorText)
	}
	if bind, err := ValidateReturnAddress(i.Endpoint, i.Listen); err != nil || bind != i.Listen {
		return fmt.Errorf(firstRunJournalListenAddressMalformedErrorText)
	}
	for _, prepared := range i.PreparedLocks {
		if prepared.Run != i.Run || prepared.Remote == firstRunJournalAbsentIdentityText ||
			!strings.HasPrefix(prepared.LocalTag, gitx.LockAttemptTagPrefix) ||
			!validJournalObjectOID(prepared.Object) {
			return fmt.Errorf(firstRunJournalPreparedLockMalformedErrorText)
		}
	}
	for tag, intent := range i.DeferredTags {
		if tag == firstRunJournalAbsentIdentityText || intent.Tag != tag ||
			(intent.Target != firstRunJournalAbsentIdentityText && !validJournalObjectOID(intent.Target)) ||
			(intent.Object != firstRunJournalAbsentIdentityText && (!validJournalObjectOID(intent.Object) || intent.Target == firstRunJournalAbsentIdentityText)) ||
			(intent.ReceiptObject == firstRunJournalAbsentIdentityText) != (intent.ReceiptCommit == firstRunJournalAbsentIdentityText) ||
			(intent.ReceiptObject != firstRunJournalAbsentIdentityText && (!validJournalObjectOID(intent.ReceiptObject) ||
				!validJournalObjectOID(intent.ReceiptCommit))) ||
			(intent.BranchHead != firstRunJournalAbsentIdentityText && !validJournalObjectOID(intent.BranchHead)) ||
			(intent.Branch != firstRunJournalAbsentIdentityText && gitx.ValidRefName(firstRunJournalBranchRefPrefixText+intent.Branch) != nil) {
			return fmt.Errorf(firstRunJournalDeferredTagMalformedErrorFormat, tag)
		}
	}
	return nil
}

func validJournalObjectOID(oid string) bool {
	if len(oid) != firstRunJournalSHA1HexCharacters && len(oid) != firstRunJournalSHA256HexCharacters {
		return firstRunJournalInvalidIdentityState
	}
	bytes, err := hex.DecodeString(oid)
	return err == nil && hex.EncodeToString(bytes) == oid
}

func (i firstRunIdentity) certificate() (tls.Certificate, error) {
	certificate, err := i.certificateIntegrity()
	if err != nil {
		return tls.Certificate{}, err
	}
	parsed, err := x509.ParseCertificate(certificate.Certificate[firstRunJournalLeafCertificateIndex])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf(firstRunJournalCertificateParseErrorFormat, err)
	}
	if time.Now().Before(parsed.NotBefore) || time.Now().After(parsed.NotAfter) {
		return tls.Certificate{}, fmt.Errorf(firstRunJournalCertificateValidityErrorText)
	}
	return certificate, nil
}

// certificateIntegrity authenticates retained identity for cleanup even
// after its TLS validity period. Binding a live listener calls certificate,
// which separately refuses an expired or not-yet-valid leaf.
func (i firstRunIdentity) certificateIntegrity() (tls.Certificate, error) {
	if i.Endpoint == firstRunJournalAbsentIdentityText || i.CertPin == firstRunJournalAbsentIdentityText || i.CertificatePEM == firstRunJournalAbsentIdentityText || i.PrivateKeyPEM == firstRunJournalAbsentIdentityText {
		return tls.Certificate{}, fmt.Errorf(firstRunJournalReturnIdentityIncompleteErrorText)
	}
	certificate, err := tls.X509KeyPair([]byte(i.CertificatePEM), []byte(i.PrivateKeyPEM))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf(firstRunJournalCertificateInvalidErrorFormat, err)
	}
	if len(certificate.Certificate) != firstRunJournalSingleLeafCount {
		return tls.Certificate{}, fmt.Errorf(firstRunJournalCertificateChainLengthErrorFormat, len(certificate.Certificate))
	}
	parsed, err := x509.ParseCertificate(certificate.Certificate[firstRunJournalLeafCertificateIndex])
	if err != nil {
		return tls.Certificate{}, err
	}
	endpoint, err := url.Parse(i.Endpoint)
	if err != nil || endpoint.Scheme != firstRunJournalReturnSchemeName || endpoint.Hostname() == firstRunJournalAbsentIdentityText ||
		parsed.VerifyHostname(endpoint.Hostname()) != nil {
		return tls.Certificate{}, fmt.Errorf(firstRunJournalCertificateEndpointMismatchErrorText)
	}
	sum := sha256.Sum256(certificate.Certificate[firstRunJournalLeafCertificateIndex])
	if i.CertPin != hex.EncodeToString(sum[:]) {
		return tls.Certificate{}, fmt.Errorf(firstRunJournalCertificatePinMismatchErrorText)
	}
	return certificate, nil
}

func encodeFirstRunCertificate(certificate tls.Certificate) (string, string, error) {
	if len(certificate.Certificate) != firstRunJournalSingleLeafCount {
		return firstRunJournalAbsentIdentityText, firstRunJournalAbsentIdentityText, fmt.Errorf(firstRunJournalCertificateLeafAbsentErrorText)
	}
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		return firstRunJournalAbsentIdentityText, firstRunJournalAbsentIdentityText, err
	}
	leaf := pem.EncodeToMemory(&pem.Block{Type: firstRunJournalCertificatePEMType, Bytes: certificate.Certificate[firstRunJournalLeafCertificateIndex]})
	private := pem.EncodeToMemory(&pem.Block{Type: firstRunJournalPrivateKeyPEMType, Bytes: key})
	return string(leaf), string(private), nil
}

func (j *firstRunJournal) close() error {
	if j == nil {
		return nil
	}
	var err error
	if j.lock != nil {
		err = j.lock.Close()
		j.lock = nil
	}
	if j.gate != nil {
		err = errors.Join(err, j.gate.Close())
		j.gate = nil
	}
	return err
}

// markCompleted retires the active locator only after the run's remote claims
// and release locks have settled. The identity itself remains for audit.
func (j *firstRunJournal) markCompleted() error {
	if j == nil || j.lock == nil || j.gate == nil {
		return fmt.Errorf(firstRunJournalJournalOwnerAbsentErrorText)
	}
	if _, err := j.read(); err != nil {
		return fmt.Errorf(firstRunJournalIdentityNoLongerReadableErrorFormat, err)
	}
	return j.clearActive()
}

// abandonUnwritten is only for identity preparation failures, before any
// release lock or mailbox ref can have been pushed.
func (j *firstRunJournal) abandonUnwritten() error {
	if j == nil || j.lock == nil || j.gate == nil {
		return fmt.Errorf(firstRunJournalJournalOwnerAbsentErrorText)
	}
	return j.clearActive()
}

func (j *firstRunJournal) clearActive() error {
	path := filepath.Join(filepath.Dir(j.dir), firstRunJournalActiveLocatorFileName)
	active, err := readActiveRun(path)
	if err != nil {
		return err
	}
	if active != j.run {
		return fmt.Errorf(firstRunJournalRunLocatorChangedErrorText)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncRunDirectory(filepath.Dir(j.dir))
}
