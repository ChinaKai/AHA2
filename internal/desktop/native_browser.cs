namespace AHADesktop {
    public static partial class Native {
        [DllImport("oleacc.dll", EntryPoint = "AccessibleObjectFromWindow")]
        static extern int BrowserAccessibleObject(IntPtr hwnd, uint id, ref Guid iid,
            [MarshalAs(UnmanagedType.Interface)] out Accessibility.IAccessible accessible);
        [DllImport("oleacc.dll", EntryPoint = "AccessibleChildren")]
        static extern int BrowserAccessibleChildren(Accessibility.IAccessible parent, int start, int count,
            [Out, MarshalAs(UnmanagedType.LPArray, SizeParamIndex = 2)] object[] children, out int obtained);
        [DllImport("oleacc.dll", EntryPoint = "WindowFromAccessibleObject")]
        static extern int BrowserAccessibleWindow(Accessibility.IAccessible accessible, out IntPtr hwnd);
        const int BrowserProtected = 0x20000000, BrowserInvisible = 0x8000, BrowserDisabled = 1, BrowserReadOnly = 0x40;
        sealed class BrowserNode {
            public Accessibility.IAccessible Accessible;
            public object Child;
            public int Depth;
            public bool ParentInvokable = false;
        }
        sealed class BrowserState {
            public BrowserNode Node;
            public IntPtr Host;
            public string HostIdentity, Identity, Name, Value, DefaultAction;
            public int Role, State;
            public Rect Bounds;
            public bool HostEnabled;
            public bool Protected { get { return (State & BrowserProtected) != 0; } }
        }
        sealed class BrowserReceipt {
            public string Target, Desktop, Kind;
            public BrowserState State;
            public long Epoch;
            public DateTime Expires;
        }
        static readonly Dictionary<string, BrowserReceipt> browserReceipts = new Dictionary<string, BrowserReceipt>();
        static bool IsBackgroundBrowser(Target target) {
            if (!string.Equals(Text(target.Window, "process"), "msedge", StringComparison.OrdinalIgnoreCase)) return false;
            var name = new StringBuilder(256);
            return GetClassName(target.Handle, name, name.Capacity) != 0 &&
                name.ToString().StartsWith("Chrome_WidgetWin_", StringComparison.Ordinal);
        }
        static string BrowserObjectIdentity(BrowserNode node) {
            IntPtr identity = Marshal.GetIUnknownForObject(node.Accessible);
            try { return identity.ToInt64().ToString("x", CultureInfo.InvariantCulture) + ":" +
                Convert.ToInt32(node.Child, CultureInfo.InvariantCulture); }
            finally { Marshal.Release(identity); }
        }
        static Target BrowserHost(Target target, Accessibility.IAccessible accessible, Dictionary<IntPtr, Target> hosts) {
            IntPtr hwnd;
            if (BrowserAccessibleWindow(accessible, out hwnd) < 0 || hwnd == IntPtr.Zero || !IsWindow(hwnd) ||
                GetAncestor(hwnd, 2) != target.Handle) Fail("browser_scope_changed");
            Target host;
            if (!hosts.TryGetValue(hwnd, out host)) {
                host = InspectWindowProcessIdentity(hwnd, false);
                hosts.Add(hwnd, host);
            }
            if (GetAncestor(hwnd, 2) != target.Handle) Fail("browser_scope_changed");
            return host;
        }
        static BrowserState InspectBrowserNode(Target target, BrowserNode node, bool readValue, Dictionary<IntPtr, Target> hosts) {
            Target host = BrowserHost(target, node.Accessible, hosts);
            var value = new BrowserState { Node = node, Host = host.Handle, HostIdentity = host.ID,
                Identity = BrowserObjectIdentity(node), Name = "", Value = "", DefaultAction = "" };
            value.HostEnabled = BackgroundControlEnabled(target, host.Handle);
            value.Role = Convert.ToInt32(node.Accessible.get_accRole(node.Child), CultureInfo.InvariantCulture);
            value.State = Convert.ToInt32(node.Accessible.get_accState(node.Child), CultureInfo.InvariantCulture);
            int x, y, width, height;
            node.Accessible.accLocation(out x, out y, out width, out height, node.Child);
            long right = (long)x + width, bottom = (long)y + height;
            if (width < 0 || height < 0 || right > int.MaxValue || right < int.MinValue ||
                bottom > int.MaxValue || bottom < int.MinValue) Fail("element_unavailable");
            value.Bounds = new Rect { Left = x, Top = y, Right = (int)right, Bottom = (int)bottom };
            if (!value.Protected && (value.State & BrowserInvisible) == 0) {
                value.Name = Bounded(node.Accessible.get_accName(node.Child));
                value.DefaultAction = Bounded(node.Accessible.get_accDefaultAction(node.Child));
                if (readValue && value.Role == 42) value.Value = Bounded(node.Accessible.get_accValue(node.Child));
            }
            if (value.State != Convert.ToInt32(node.Accessible.get_accState(node.Child), CultureInfo.InvariantCulture))
                Fail("refresh_required");
            return value;
        }
        static bool SameBrowserState(BrowserState a, BrowserState b) {
            return a.Host == b.Host && a.HostIdentity == b.HostIdentity && a.Identity == b.Identity && a.HostEnabled == b.HostEnabled &&
                a.Role == b.Role && a.State == b.State && a.Name == b.Name && a.Value == b.Value &&
                a.DefaultAction == b.DefaultAction && a.Bounds.Left == b.Bounds.Left && a.Bounds.Top == b.Bounds.Top &&
                a.Bounds.Right == b.Bounds.Right && a.Bounds.Bottom == b.Bounds.Bottom;
        }
        static string BrowserAction(BrowserState state, Rect frame) {
            if (!state.HostEnabled || state.Protected || (state.State & (BrowserDisabled | BrowserReadOnly | BrowserInvisible)) != 0 ||
                state.Bounds.Right <= state.Bounds.Left || state.Bounds.Bottom <= state.Bounds.Top ||
                state.Bounds.Left < frame.Left || state.Bounds.Top < frame.Top ||
                state.Bounds.Right > frame.Right || state.Bounds.Bottom > frame.Bottom) return "";
            if (state.Role == 42) return "set_value";
            if ((state.Role == 30 || state.Role == 43 || state.Role == 12 || state.Role == 44 || state.Role == 45) &&
                state.DefaultAction != "") return "invoke";
            return "";
        }
        static string BrowserRole(BrowserState state) {
            if (state.Protected) return "password";
            switch (state.Role) {
                case 42: return "ControlType.Edit";
                case 43: return "ControlType.Button";
                case 30: return "ControlType.Hyperlink";
                case 12: return "ControlType.MenuItem";
                case 44: return "ControlType.CheckBox";
                case 45: return "ControlType.RadioButton";
                case 41: return "ControlType.Text";
                case 40: return "ControlType.Image";
                case 15: return "ControlType.Document";
                default: return "ControlType.Custom";
            }
        }
        static List<BrowserState> ReadBackgroundBrowser(Target target, Rect frame, List<object> descriptions, long epoch) {
            var result = new List<BrowserState>();
            if (!IsBackgroundBrowser(target)) return result;
            try {
                var handles = new List<IntPtr> { target.Handle };
                string failed = "";
                EnumChildWindows(target.Handle, delegate(IntPtr hwnd, IntPtr unused) {
                    try {
                        if (handles.Count >= MaxElements) Fail("element_limit");
                        if (!IsWindowVisible(hwnd)) return true;
                        InspectBackgroundControl(target, hwnd);
                        handles.Add(hwnd);
                        return true;
                    } catch (NativeFailure error) { failed = error.Code; return false; }
                    catch { failed = "browser_accessibility_unavailable"; return false; }
                }, IntPtr.Zero);
                if (failed != "") Fail(failed);
                var pending = new Queue<BrowserNode>();
                foreach (IntPtr hwnd in handles) {
                    backgroundLifetime.WatchBrowser(hwnd);
                    Guid iid = new Guid("618736E0-3C3D-11CF-810C-00AA00389B71");
                    Accessibility.IAccessible accessible;
                    if (BrowserAccessibleObject(hwnd, 0xfffffffc, ref iid, out accessible) == 0 && accessible != null)
                        pending.Enqueue(new BrowserNode { Accessible = accessible, Child = 0, Depth = 0 });
                }
                var seen = new HashSet<string>();
                var hosts = new Dictionary<IntPtr, Target>();
                int nodes = 0;
                while (pending.Count > 0) {
                    if (++nodes > MaxNodes) Fail("element_limit");
                    BrowserNode node = pending.Dequeue();
                    if (node.Depth > 64) Fail("element_limit");
                    if (!seen.Add(BrowserObjectIdentity(node))) continue;
                    BrowserState state = InspectBrowserNode(target, node, true, hosts);
                    if ((state.State & BrowserInvisible) != 0) continue;
                    backgroundLifetime.WatchBrowser(state.Host);
                    if (descriptions.Count >= MaxElements) Fail("element_limit");
                    string kind = BrowserAction(state, frame), id = "readonly:" + Guid.NewGuid().ToString("N");
                    var actions = new List<string>();
                    if (kind != "") {
                        if (browserReceipts.Count >= 8192) Fail("element_limit");
                        id = "browser:" + Guid.NewGuid().ToString("N");
                        actions.Add(kind);
                        browserReceipts.Add(id, new BrowserReceipt { Target = target.ID,
                            Desktop = Text(target.Window, "desktop_id"), State = state, Kind = kind, Epoch = epoch,
                            Expires = DateTime.UtcNow.AddSeconds(30) });
                    }
                    // A button/link's text is already its accessible name. Do not
                    // let a smaller readonly caption hide that actionable hitbox.
                    if (state.Protected || !node.ParentInvokable || (state.Role != 41 && state.Role != 40))
                        descriptions.Add(Obj("id", id, "name", state.Name, "value", state.Value, "role", BrowserRole(state),
                            "actions", actions, "x", (double)state.Bounds.Left - frame.Left, "y", (double)state.Bounds.Top - frame.Top,
                            "width", (double)state.Bounds.Right - state.Bounds.Left, "height", (double)state.Bounds.Bottom - state.Bounds.Top));
                    result.Add(state);
                    if (state.Protected || Convert.ToInt32(node.Child, CultureInfo.InvariantCulture) != 0) continue;
                    int count = node.Accessible.accChildCount;
                    if (count < 0 || count > MaxNodes || nodes + pending.Count + count > MaxNodes) Fail("element_limit");
                    if (count == 0) continue;
                    var children = new object[count];
                    int obtained;
                    if (BrowserAccessibleChildren(node.Accessible, 0, count, children, out obtained) < 0 ||
                        obtained < 0 || obtained > count) Fail("browser_accessibility_unavailable");
                    for (int i = 0; i < obtained; i++) {
                        var child = children[i] as Accessibility.IAccessible;
                        bool parentInvokable = kind == "invoke" || node.ParentInvokable;
                        if (child != null) pending.Enqueue(new BrowserNode { Accessible = child, Child = 0,
                            Depth = node.Depth + 1, ParentInvokable = parentInvokable });
                        else if (children[i] is int) pending.Enqueue(new BrowserNode {
                            Accessible = node.Accessible, Child = children[i], Depth = node.Depth + 1, ParentInvokable = parentInvokable });
                        else Fail("browser_accessibility_unavailable");
                    }
                }
                if (result.Count == 0) Fail("browser_accessibility_unavailable");
                bool page = false;
                foreach (var state in result)
                    page |= state.Role == 15 && state.Name != "" &&
                        state.Bounds.Right > state.Bounds.Left && state.Bounds.Bottom > state.Bounds.Top;
                if (!page) Fail("browser_page_unavailable");
                return result;
            } catch (NativeFailure) { throw; }
            catch { Fail("browser_accessibility_unavailable"); return null; }
        }
        static void ValidateBackgroundBrowser(Target target, List<BrowserState> states) {
            try {
                // Rebuild the host cache for post-capture validation: each native
                // process lifetime is rechecked, without repeating token queries
                // for every virtual node in that same host.
                var hosts = new Dictionary<IntPtr, Target>();
                foreach (var state in states)
                    if (!SameBrowserState(state, InspectBrowserNode(target, state.Node, true, hosts))) Fail("refresh_required");
            } catch (NativeFailure) { throw; }
            catch { Fail("browser_accessibility_unavailable"); }
        }
        static void PruneBrowserReceipts() {
            var expired = new List<string>();
            foreach (var item in browserReceipts) if (item.Value.Expires <= DateTime.UtcNow) expired.Add(item.Key);
            foreach (string id in expired) browserReceipts.Remove(id);
        }
        static void ActBackgroundBrowser(Target target, Dictionary<string, object> action) {
            string id = Text(action, "element_id"), kind = Text(action, "kind"), value = Text(action, "value");
            if (!IsBackgroundBrowser(target)) Fail("unsupported_action");
            BrowserReceipt receipt;
            if (!browserReceipts.TryGetValue(id, out receipt)) Fail("refresh_required");
            browserReceipts.Remove(id);
            if (receipt.Target != target.ID || receipt.Desktop != Text(target.Window, "desktop_id") ||
                receipt.Expires <= DateTime.UtcNow || kind != receipt.Kind) Fail("refresh_required");
            target.Revalidate();
            CheckBackgroundEpoch(receipt.Epoch);
            try {
                BrowserState state = InspectBrowserNode(target, receipt.State.Node, true, new Dictionary<IntPtr, Target>());
                if (state.Protected) Fail("password_element");
                if (!state.HostEnabled || (state.State & BrowserDisabled) != 0) Fail("element_disabled");
                if ((state.State & BrowserReadOnly) != 0) Fail("element_read_only");
                Rect frame;
                if (!GetWindowRect(target.Handle, out frame)) Fail("stale_target");
                if (!SameBrowserState(receipt.State, state) || BrowserAction(state, frame) != kind) Fail("refresh_required");
                target.Revalidate();
                CheckBackgroundEpoch(receipt.Epoch);
                if (kind == "invoke") state.Node.Accessible.accDoDefaultAction(state.Node.Child);
                else if (kind == "set_value") state.Node.Accessible.set_accValue(state.Node.Child, value);
                else Fail("unsupported_action");
                Thread.Sleep(150);
                target.Revalidate();
            } catch (NativeFailure) { throw; }
            catch { Fail("browser_accessibility_unavailable"); }
        }
    }
}
