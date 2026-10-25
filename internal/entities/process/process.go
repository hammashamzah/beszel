// Package process holds the per-process stats an agent reports for hosts
// whose workloads are plain processes rather than containers.
package process

// Proc is one running process in the agent's top list, like a row in htop.
type Proc struct {
	Pid   int32   `json:"p" cbor:"0,keyasint"`
	User  string  `json:"u" cbor:"1,keyasint"`
	Name  string  `json:"n" cbor:"2,keyasint"`
	Cmd   string  `json:"cmd,omitempty" cbor:"3,keyasint,omitempty"`
	Group string  `json:"g" cbor:"4,keyasint"`
	Cpu   float64 `json:"c" cbor:"5,keyasint"`                   // percent of the whole host, as for containers
	Mem   uint64  `json:"m" cbor:"6,keyasint"`                   // resident memory in bytes
	Start int64   `json:"t,omitzero" cbor:"7,keyasint,omitzero"` // unix seconds
}
