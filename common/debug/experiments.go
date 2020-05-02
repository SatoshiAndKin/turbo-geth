package debug

import (
	"os"
	"sync"
	"sync/atomic"
)

var storeAccRootEnv sync.Once
var storeAccRoot bool
var disableIHEnv sync.Once
var disableIH bool

// atomic: bit 0 is the value, bit 1 is the initialized flag
var getNodeData uint32

const (
	gndValueFlag = 1 << iota
	gndInitializedFlag
)

// IsGetNodeData indicates whether the GetNodeData functionality should be enabled.
// By default that's driven by the presence or absence of DISABLE_GET_NODE_DATA environment variable.
func IsGetNodeData() bool {
	x := atomic.LoadUint32(&getNodeData)
	if x&gndInitializedFlag != 0 { // already initialized
		return x&gndValueFlag != 0
	}

	RestoreGetNodeData()
	return IsGetNodeData()
}

// RestoreGetNodeData enables or disables the GetNodeData functionality
// according to the presence or absence of DISABLE_GET_NODE_DATA environment variable.
func RestoreGetNodeData() {
	_, envVarSet := os.LookupEnv("DISABLE_GET_NODE_DATA")
	OverrideGetNodeData(!envVarSet)
}

// OverrideGetNodeData allows to explicitly enable or disable the GetNodeData functionality.
func OverrideGetNodeData(val bool) {
	if val {
		atomic.StoreUint32(&getNodeData, gndInitializedFlag|gndValueFlag)
	} else {
		atomic.StoreUint32(&getNodeData, gndInitializedFlag)
	}
}
