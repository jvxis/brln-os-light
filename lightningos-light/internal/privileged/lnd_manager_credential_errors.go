package privileged

import "errors"

// Only fixed, non-sensitive diagnostics cross the broker boundary. In
// particular, never forward RPC errors, config contents or credential bytes.
type lndCredentialError string

func (err lndCredentialError) Error() string {
	_, message := LNDManagerCredentialDiagnostic(err)
	return message
}

func LNDManagerCredentialDiagnostic(err error) (string, string) {
	var diagnostic lndCredentialError
	if errors.As(err, &diagnostic) {
		switch diagnostic {
		case "admin_macaroon_mode":
			return string(diagnostic), "LND admin macaroon must have mode 0600 or 0640; verify its service ownership before retrying"
		case "admin_macaroon_owner":
			return string(diagnostic), "LND admin macaroon owner/group must match the User/Group of lnd.service"
		case "lnd_unit_identity":
			return string(diagnostic), "LND service User/Group could not be validated"
		case "macaroon_path_unsupported":
			return string(diagnostic), "Configured LND macaroon path is unsupported"
		case "transaction_incomplete":
			return string(diagnostic), "LND manager credential transaction is incomplete; recover its trusted state before retrying"
		case "rpc_failed":
			return string(diagnostic), "LND manager credential RPC verification failed"
		}
	}
	return "lnd_manager_credential_failed", "LND manager credential operation failed"
}

type lndCredentialFileError string

func (err lndCredentialFileError) Error() string {
	return "LND manager credential file " + string(err) + " is unsafe"
}
