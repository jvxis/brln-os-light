package privileged

import "errors"

// Broker error codes the Manager can act upon. Their messages are fixed text:
// they never carry paths, credential bytes, macaroon root key IDs or RPC errors.
const (
	ErrorCodeAppRemoveFailed                   = "app_remove_failed"
	ErrorCodeAppCatalogMismatch                = "app_catalog_mismatch"
	ErrorCodeLNbitsCredentialRevokeFailed      = "lnbits_credential_revoke_failed"
	ErrorCodeLNbitsCredentialEnrollmentMissing = "lnbits_credential_enrollment_missing"
	ErrorCodeLNbitsCredentialLNDUnavailable    = "lnbits_credential_lnd_unavailable"
)

var (
	// errAppCatalogMismatch classifies a readable Manager-owned declaration
	// whose content differs from what the current closed catalog derives. The
	// validation itself is unchanged; only the diagnostic is classified.
	errAppCatalogMismatch = errors.New("app declaration does not match the catalog")
	// errLNbitsCredentialNotEnrolled classifies a legacy LNbits credential that
	// this broker never enrolled in its root-owned execution snapshot. Its
	// automatic revocation is refused on purpose.
	errLNbitsCredentialNotEnrolled = errors.New("LNbits credential is not enrolled by this broker")
	// errLNbitsCredentialLNDUnavailable classifies an LND that could not be
	// queried for the legacy LNbits credential migration.
	errLNbitsCredentialLNDUnavailable = errors.New("LND unavailable for LNbits credential migration")
)

// classifiedError keeps a validator's existing fixed diagnostic text while
// letting the broker map it to a specific response code with errors.Is.
type classifiedError struct {
	message string
	class   error
}

func (err classifiedError) Error() string { return err.message }

func (err classifiedError) Is(target error) bool { return target == err.class }

func catalogMismatchError(message string) error {
	return classifiedError{message: message, class: errAppCatalogMismatch}
}

func lnbitsCredentialNotEnrolledError(message string) error {
	return classifiedError{message: message, class: errLNbitsCredentialNotEnrolled}
}

func lnbitsCredentialLNDUnavailableError(message string) error {
	return classifiedError{message: message, class: errLNbitsCredentialLNDUnavailable}
}

// AppRemoveDiagnostic maps an app removal failure to a fixed response code and
// message. Anything not explicitly classified stays generic.
func AppRemoveDiagnostic(err error) (string, string) {
	if errors.Is(err, errAppCatalogMismatch) {
		return ErrorCodeAppCatalogMismatch, "app declaration does not match the current catalog; start the app once so LightningOS refreshes it, then uninstall again"
	}
	return ErrorCodeAppRemoveFailed, "app remove operation failed"
}

// LNbitsCredentialRevokeDiagnostic maps a legacy LNbits credential revocation
// failure to a fixed response code and message. No general macaroon authority
// is granted to the Manager in any case.
func LNbitsCredentialRevokeDiagnostic(err error) (string, string) {
	switch {
	case errors.Is(err, errLNbitsCredentialNotEnrolled):
		return ErrorCodeLNbitsCredentialEnrollmentMissing, "LNbits legacy credential is not enrolled by this LightningOS broker; it is not revoked automatically and requires operator review"
	case errors.Is(err, errLNbitsCredentialLNDUnavailable):
		return ErrorCodeLNbitsCredentialLNDUnavailable, "LND is unavailable for the LNbits legacy credential migration; retry when LND is running and unlocked"
	}
	return ErrorCodeLNbitsCredentialRevokeFailed, "LNbits legacy credential revocation failed; no general macaroon authority is granted to the Manager"
}
