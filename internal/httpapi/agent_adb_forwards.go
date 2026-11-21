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
// through the Agent ADB executor, and which Task hardware binding currently
// owns each name. adb keeps one host-wide listener table keyed only by the
// local name: forward --remove compares that name and not the device
// transport, and a successful plain add rebinds an existing same-name
// listener to the caller's transport. Without this record one Task could
// delete another device's forward just by knowing (or reading from --list)
// the port name; with a per-scope record only, a rebind-permitting add would
// also leave the previous owner's name removable against the new listener.
//
// A successful add therefore takes ownership of its local name away from every
// other binding. That invalidation happens on the add path itself, not from
// the later --remove exit code, so a rebind can never leave an older record
// authorized. A successful remove drops the name from its own scope.
//
// The registry is deliberately in-process. After a restart previously created
// forwards are no longer removable through the Agent API until they are
// created again; that is stricter, not permissive. A forward added or removed
// outside this path (owner on the host, adb server restart) can leave a stale
// entry for a reused port; see the review packet for that residual risk.
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
	// An add that reaches the registry succeeded, and adb may have rebound an
	// existing listener for this local name to the current transport whatever
	// form was used (--no-rebind succeeds only when nothing was bound). The
	// previous owner's record no longer describes a listener it can remove.
	for otherScope, locals := range registry.byScope {
		if otherScope == scope {
			continue
		}
		delete(locals, local)
		if len(locals) == 0 {
			delete(registry.byScope, otherScope)
		}
	}
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
