package httpapi

import (
	"strconv"
	"strings"
	"sync"

	"github.com/ChinaKai/AHA2/internal/hardware"
)

// adbForwardScope names one Task hardware binding for the forward registry.
// The configured target is part of the key: if the owner re-points the same
// group at another device, forwards created against the old target must not
// become removable through the new binding.
func adbForwardScope(taskID, groupID, target string) string {
	return taskID + "\x00" + groupID + "\x00" + target
}

// registeredADBForwardLocal is the local name recorded after a successful
// add. adb prints the port it picked for "tcp:0" instead of storing tcp:0 as
// the listener name, so that port is what a later --remove must name. Any
// unexpected output leaves the forward unrecorded, which only makes the later
// remove stricter.
func registeredADBForwardLocal(local string, result hardware.ADBExecResult) string {
	if local != "tcp:0" {
		return local
	}
	value := strings.TrimSpace(string(result.Stdout))
	if value == "" {
		return ""
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return ""
		}
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return ""
	}
	return "tcp:" + strconv.Itoa(port)
}

// adbForwardRegistry records the forward local names this AHA process created
// through the Agent ADB executor, scoped to one Task hardware binding. adb
// forward --remove compares only the local name and not the device transport,
// so without this record one Task could delete another device's forward just
// by knowing (or reading from --list) the port name.
//
// The registry is deliberately in-process. After a restart previously created
// forwards are no longer removable through the Agent API until they are
// created again; that is stricter, not permissive. A forward removed outside
// this path (owner on the host, adb server restart) can leave a stale entry
// for a reused port; see the review packet for that residual risk.
type adbForwardRegistry struct {
	mu      sync.Mutex
	byScope map[string]map[string]struct{}
}

func newADBForwardRegistry() *adbForwardRegistry {
	return &adbForwardRegistry{byScope: map[string]map[string]struct{}{}}
}

func (registry *adbForwardRegistry) add(scope, local string) {
	if registry == nil || scope == "" || local == "" {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	locals := registry.byScope[scope]
	if locals == nil {
		locals = map[string]struct{}{}
		registry.byScope[scope] = locals
	}
	locals[local] = struct{}{}
}

func (registry *adbForwardRegistry) remove(scope, local string) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	locals := registry.byScope[scope]
	delete(locals, local)
	if len(locals) == 0 {
		delete(registry.byScope, scope)
	}
}

func (registry *adbForwardRegistry) allows(scope, local string) bool {
	if registry == nil || scope == "" || local == "" {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	_, ok := registry.byScope[scope][local]
	return ok
}
