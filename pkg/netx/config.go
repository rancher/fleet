package netx

// TLSConfig holds TLS settings for outbound connections.
type TLSConfig struct {
	// CABundle holds PEM CAs trusted in addition to the system pool.
	CABundle           []byte
	InsecureSkipVerify bool
}
