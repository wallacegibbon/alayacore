// Package stream provides the SliceBuffer IO bridge used to connect
// adapters (terminal/plainio/rawio) with the agent session.
//
// SliceBuffer is an io.ReadWriteCloser that bridges slice-oriented writes
// (each Write call sends an atomic slice) with byte-oriented reads
// (Read buffers slices into a continuous byte stream).
//
// TLV frame encoding and decoding have moved to the tlv package.
// System message types and tool data structures have moved to the protocol package.
package stream
