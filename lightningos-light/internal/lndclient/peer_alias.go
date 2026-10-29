package lndclient

import "strings"

// DisplayPeerAlias excludes LND lookup errors stored in the alias field.
// Real node aliases fit in 32 bytes. Preserve even error-like valid aliases;
// only treat longer lookup-error placeholders as missing metadata.
func DisplayPeerAlias(alias string) string {
	alias = strings.TrimSpace(alias)
	if len(alias) > 32 && strings.HasPrefix(strings.ToLower(alias), "unable to lookup peer alias:") {
		return ""
	}
	return alias
}
