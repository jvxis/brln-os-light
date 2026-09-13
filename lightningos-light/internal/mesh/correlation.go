package mesh

// CorrelatedReply is a new, explicitly negotiated data kind. Legacy peers never
// receive it: capability exchange is authenticated with the existing peer key.
const CorrelatedReply byte = 9
const Capabilities byte = 10
const ReplyHeader = 49

func EncodeReply(kind byte, request Packet, content []byte) ([]byte, error) {
	if (kind != Transaction && kind != Invoice) || request.Session == ([16]byte{}) || request.Hash == ([32]byte{}) || len(content) == 0 || len(content) > MaxContent-ReplyHeader {
		return nil, ErrPacket
	}
	raw := make([]byte, ReplyHeader, len(content)+ReplyHeader)
	raw[0] = kind
	copy(raw[1:17], request.Session[:])
	copy(raw[17:49], request.Hash[:])
	return append(raw, content...), nil
}
func DecodeReply(raw []byte) (kind byte, session [16]byte, hash [32]byte, content []byte, err error) {
	if len(raw) <= ReplyHeader || len(raw) > MaxContent || (raw[0] != Transaction && raw[0] != Invoice) {
		err = ErrPacket
		return
	}
	kind = raw[0]
	copy(session[:], raw[1:17])
	copy(hash[:], raw[17:49])
	content = raw[49:]
	if session == ([16]byte{}) || hash == ([32]byte{}) {
		err = ErrPacket
	}
	return
}
