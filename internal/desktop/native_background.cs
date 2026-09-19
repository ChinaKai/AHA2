namespace AHADesktop {
    public static partial class Native {
        [DllImport("user32.dll", EntryPoint = "SendMessageTimeoutW", CharSet = CharSet.Unicode)]
        static extern IntPtr ReadControlText(IntPtr hwnd, uint message, IntPtr capacity, StringBuilder text,
            uint flags, uint timeout, out IntPtr result);
        sealed class BackgroundControl {
            public IntPtr Handle, Parent;
            public string ID, Class, Kind, ProcessIdentity;
            public bool Password, Enabled, ReadOnly, ForeignProcess;
            public int Style, ControlID;
            public uint Thread;
            public Rect Bounds;
        }
        static bool BackgroundControlEnabled(Target target, IntPtr hwnd) {
            bool enabled = true;
            for (int i = 0; i < 64 && hwnd != IntPtr.Zero; i++, hwnd = GetParent(hwnd)) {
                enabled = enabled && IsWindowEnabled(hwnd);
                if (hwnd == target.Handle) return enabled;
            }
            Fail("background_control_changed"); return false;
        }
        static bool SameBackgroundControl(BackgroundControl before, BackgroundControl after) {
            return before.ID == after.ID && before.Parent == after.Parent && before.Thread == after.Thread &&
                before.ControlID == after.ControlID && before.ProcessIdentity == after.ProcessIdentity &&
                before.ForeignProcess == after.ForeignProcess && before.Class == after.Class && before.Style == after.Style &&
                before.Password == after.Password && before.Enabled == after.Enabled && before.ReadOnly == after.ReadOnly &&
                before.Bounds.Left == after.Bounds.Left && before.Bounds.Top == after.Bounds.Top &&
                before.Bounds.Right == after.Bounds.Right && before.Bounds.Bottom == after.Bounds.Bottom;
        }
        static BackgroundControl InspectBackgroundControl(Target target, IntPtr hwnd) {
            uint pid;
            uint thread = GetWindowThreadProcessId(hwnd, out pid);
            if (!IsWindow(hwnd) || pid == 0 || thread == 0 || GetAncestor(hwnd, 2) != target.Handle)
                Fail("background_control_changed");
            bool foreign = pid != target.PID;
            string processIdentity = target.ID;
            if (foreign) {
                // Browser render surfaces can be native children of another
                // process. Verify trust/lifetime without reading any child text.
                Target child;
                try { child = InspectWindowProcessIdentity(hwnd, false); }
                catch { Fail("background_child_unverifiable"); return null; }
                if (child.PID != pid || child.Thread != thread || GetAncestor(hwnd, 2) != target.Handle)
                    Fail("background_control_changed");
                processIdentity = child.ID;
            }
            if (!IsWindowVisible(hwnd)) Fail("element_unavailable");
            var name = new StringBuilder(256);
            if (GetClassName(hwnd, name, name.Capacity) == 0) Fail("element_unavailable");
            string cls = name.ToString();
            int style = GetWindowLong(hwnd, -16), buttonStyle = style & 0xf;
            bool edit = cls.Equals("Edit", StringComparison.OrdinalIgnoreCase) ||
                cls.StartsWith("WindowsForms10.EDIT.", StringComparison.OrdinalIgnoreCase) ||
                cls.Equals("RichEdit20W", StringComparison.OrdinalIgnoreCase) ||
                cls.Equals("RICHEDIT50W", StringComparison.OrdinalIgnoreCase);
            bool formsButton = cls.StartsWith("WindowsForms10.BUTTON.", StringComparison.OrdinalIgnoreCase);
            bool button = formsButton || cls.Equals("Button", StringComparison.OrdinalIgnoreCase);
            string kind = edit ? "set_value" : "";
            if (button) {
                if (buttonStyle == 0 || buttonStyle == 1) kind = "invoke";
                // Owner-drawn controls have no trustworthy role from HWND styles.
                // Do not guess checkbox/radio semantics or fall back to UIA.
                if (formsButton && (buttonStyle == 2 || buttonStyle == 3 || buttonStyle == 5 || buttonStyle == 6)) kind = "toggle";
                if (formsButton && (buttonStyle == 4 || buttonStyle == 9)) kind = "select";
            }
            Rect bounds;
            if (!GetWindowRect(hwnd, out bounds)) Fail("element_unavailable");
            return new BackgroundControl { Handle = hwnd, Parent = GetParent(hwnd), Thread = thread, ControlID = GetDlgCtrlID(hwnd),
                ID = "native:" + hwnd.ToInt64().ToString("x", CultureInfo.InvariantCulture),
                Class = cls, Kind = kind, ProcessIdentity = processIdentity, ForeignProcess = foreign,
                Password = edit && (style & 0x20) != 0, ReadOnly = edit && (style & 0x800) != 0,
                Enabled = BackgroundControlEnabled(target, hwnd), Style = style, Bounds = bounds };
        }
        static List<BackgroundControl> BackgroundControls(Target target) {
            var result = new List<BackgroundControl>();
            int nodes = 0;
            string failed = "";
            EnumChildWindows(target.Handle, delegate(IntPtr hwnd, IntPtr unused) {
                try {
                    if (++nodes > MaxNodes || result.Count >= MaxElements) Fail("element_limit");
                    if (!IsWindowVisible(hwnd)) return true;
                    result.Add(InspectBackgroundControl(target, hwnd));
                    return true;
                } catch (NativeFailure error) { failed = error.Code; return false; }
                catch { failed = "element_unavailable"; return false; }
            }, IntPtr.Zero);
            if (failed != "") Fail(failed);
            return result;
        }
        static object ObserveBackgroundNative(Target target) {
            PrepareBackgroundLifetime(target);
            target.Revalidate();
            Rect rect;
            if (!GetWindowRect(target.Handle, out rect)) Fail("stale_target");
            var descriptions = new List<object>();
            var controls = BackgroundControls(target);
            foreach (var control in controls) backgroundLifetime.Watch(control.Handle);
            long epoch = backgroundLifetime.Read();
            foreach (var control in controls) {
                target.Revalidate();
                CheckBackgroundEpoch(epoch);
                if (!SameBackgroundControl(control, InspectBackgroundControl(target, control.Handle))) Fail("refresh_required");
                var actions = new List<string>();
                if (!control.ForeignProcess && !control.Password && control.Enabled && !control.ReadOnly && control.Kind != "") actions.Add(control.Kind);
                string value = "";
                if (!control.ForeignProcess && !control.Password && control.Kind != "") {
                    var text = new StringBuilder(2049);
                    IntPtr result;
                    if (ReadControlText(control.Handle, 0x000D, new IntPtr(text.Capacity), text, 3, 500, out result) == IntPtr.Zero)
                        Fail("element_unavailable");
                    if (!SameBackgroundControl(control, InspectBackgroundControl(target, control.Handle))) Fail("refresh_required");
                    value = Bounded(text.ToString());
                }
                string id = actions.Count == 0 ? "readonly:" + Guid.NewGuid().ToString("N") :
                    RememberBackgroundControl(target, control, epoch);
                descriptions.Add(Obj("id", id, "name", control.Password || control.Kind == "set_value" ? "" : value,
                    "role", control.Password ? "password" : control.Kind == "set_value" ? "ControlType.Edit" :
                        control.Kind == "invoke" ? "ControlType.Button" : control.Kind == "toggle" ? "ControlType.CheckBox" :
                        control.Kind == "select" ? "ControlType.RadioButton" : "ControlType.Custom",
                    "value", control.Password ? "" : value, "actions", actions,
                    "x", (double)control.Bounds.Left - rect.Left, "y", (double)control.Bounds.Top - rect.Top,
                    "width", (double)control.Bounds.Right - control.Bounds.Left,
                    "height", (double)control.Bounds.Bottom - control.Bounds.Top));
            }
            var browser = ReadBackgroundBrowser(target, rect, descriptions, epoch);
            string error;
            target.Revalidate();
            CheckBackgroundEpoch(epoch);
            string image = Capture(target, rect, descriptions, out error);
            var after = BackgroundControls(target);
            if (after.Count != controls.Count) Fail("refresh_required");
            for (int i = 0; i < controls.Count; i++)
                if (!SameBackgroundControl(controls[i], after[i])) Fail("refresh_required");
            Rect finalRect;
            if (!GetWindowRect(target.Handle, out finalRect) || rect.Left != finalRect.Left || rect.Top != finalRect.Top ||
                rect.Right != finalRect.Right || rect.Bottom != finalRect.Bottom) Fail("refresh_required");
            target.Revalidate();
            ValidateBackgroundBrowser(target, browser);
            CheckBackgroundEpoch(epoch);
            return Obj("window", target.Window, "elements", descriptions, "image", image,
                "width", Math.Max(0L, (long)rect.Right - rect.Left), "height", Math.Max(0L, (long)rect.Bottom - rect.Top),
                "capture_error", error);
        }
        static void ActBackgroundNative(Target target, Dictionary<string, object> action) {
            string id = Text(action, "element_id"), kind = Text(action, "kind"), value = Text(action, "value");
            if (id == "" || id.Length > 1024 || value.Length > 32768 || kind != "set_value" && value != "") Fail("invalid_request");
            if (id.StartsWith("browser:", StringComparison.Ordinal)) {
                ActBackgroundBrowser(target, action);
                return;
            }
            var receipt = TakeBackgroundReceipt(target, id);
            var selected = receipt.Control;
            target.Revalidate();
            var current = InspectBackgroundControl(target, selected.Handle);
            if (!SameBackgroundControl(selected, current)) Fail("refresh_required");
            selected = current;
            if (selected.ForeignProcess) Fail("unsupported_action");
            if (selected.Password) Fail("password_element");
            if (!selected.Enabled) Fail("element_disabled");
            if (selected.ReadOnly) Fail("element_read_only");
            if (selected.Kind == "" || selected.Kind != kind) Fail("unsupported_action");
            CheckBackgroundEpoch(receipt.Epoch);
            target.Revalidate();
            IntPtr result;
            if (kind == "set_value") {
                if (SendTextTimeout(selected.Handle, 0x000C, IntPtr.Zero, value, 3, 1500, out result) == IntPtr.Zero ||
                    result == IntPtr.Zero) Fail("native_unavailable");
            } else if (selected.Class.StartsWith("WindowsForms10.BUTTON.", StringComparison.OrdinalIgnoreCase)) {
                if (kind == "select") {
                    if (SendControlTimeout(selected.Handle, 0x00F0, IntPtr.Zero, IntPtr.Zero, 3, 1500, out result) == IntPtr.Zero)
                        Fail("native_unavailable");
                    if (result.ToInt64() == 1) return;
                }
                if (SendControlTimeout(selected.Handle, 0x00F5, IntPtr.Zero, IntPtr.Zero, 3, 1500, out result) == IntPtr.Zero)
                    Fail("native_unavailable");
            } else {
                IntPtr parent = GetParent(selected.Handle);
                uint pid;
                GetWindowThreadProcessId(parent, out pid);
                if (parent == IntPtr.Zero || pid != target.PID || GetAncestor(parent, 2) != target.Handle) Fail("background_control_changed");
                if (SendControlTimeout(parent, 0x0111, new IntPtr(GetDlgCtrlID(selected.Handle) & 0xffff),
                    selected.Handle, 3, 1500, out result) == IntPtr.Zero) Fail("native_unavailable");
            }
            Thread.Sleep(100);
        }
        // Only read-only desktop queries belong here. Selection never switches or activates.
        static Target ResolveBackground(string id, Guid bound, Guid active, bool selecting) {
            InputDesktopGuard();
            if (CurrentVirtualDesktop() != active) Fail("background_context_changed");
            IntPtr hwnd = WindowHandle(id);
            Guid desktop;
            bool current;
            if (!TryWindowDesktop(hwnd, out desktop, out current)) Fail("desktop_identity_unavailable");
            if (bound != Guid.Empty && desktop != bound) Fail("background_desktop_changed");
            if (current != (desktop == active)) Fail("desktop_identity_unavailable");
            if (!current) {
                Guid verified;
                var known = VerifiedDesktopOrder(out verified);
                if (verified != active) Fail("background_context_changed");
                if (!known.Contains(desktop)) Fail("desktop_identity_unavailable");
            }
            Target target = selecting ? InspectPickerWindow(hwnd, current ? 0 : 2) :
                InspectVisualWindow(hwnd, current ? 0 : 2);
            if (target.ID != id) Fail("stale_target");
            Guid after;
            bool stillCurrent;
            if (!TryWindowDesktop(hwnd, out after, out stillCurrent) || after != desktop || stillCurrent != current)
                Fail("background_desktop_changed");
            target.Window["kind"] = "window";
            target.Window["desktop_id"] = desktop.ToString("D");
            target.Window["other_desktop"] = !current;
            target.IncludeOffscreen = !current;
            target.Revalidate = delegate { ResolveBackground(id, desktop, active, false); };
            return target;
        }
        public static string RunBackground(string requestJSON) {
            try {
                if (SetThreadDpiAwarenessContext(new IntPtr(-4)) == IntPtr.Zero) Fail("native_unavailable");
                InputDesktopGuard();
                var request = JSON.Deserialize<Dictionary<string, object>>(requestJSON);
                string operation = Text(request, "operation"), id = Text(request, "window_id");
                if (operation != "background_select" && operation != "background_observe" &&
                    operation != "background_act") Fail("invalid_request");
                Guid bound = Guid.Empty;
                if (operation != "background_select" &&
                    (!Guid.TryParse(Text(request, "desktop_id"), out bound) || bound == Guid.Empty))
                    Fail("invalid_request");
                Guid active = CurrentVirtualDesktop();
                Target target = ResolveBackground(id, bound, active, operation == "background_select");
                // The focus guard exists to catch one of our actions moving the
                // user's focus. A read cannot move focus, so a change observed
                // while selecting or observing is the Owner using their own
                // machine, and failing the read would punish them for that.
                // Only the mutating operation is policed.
                bool guardFocus = operation == "background_act";
                Focus focus = guardFocus ? Focus.Read(target.Thread) : null;
                System.Action validate = target.Revalidate;
                target.Revalidate = guardFocus ? delegate { validate(); focus.Check(); } : validate;
                try {
                    object result;
                    if (operation == "background_select") result = Obj("ok", true, "window", target.Window);
                    else if (operation == "background_observe") result = Obj("ok", true, "observation", ObserveBackgroundNative(target));
                    else {
                        var action = request["action"] as Dictionary<string, object>;
                        if (action == null) Fail("invalid_request");
                        ActBackgroundNative(target, action);
                        result = Obj("ok", true);
                    }
                    target.Revalidate();
                    return JSON.Serialize(result);
                } finally {
                    if (guardFocus) focus.Check();
                    InputDesktopGuard();
                    if (CurrentVirtualDesktop() != active) Fail("background_context_changed");
                }
            } catch (NativeFailure error) { return JSON.Serialize(Obj("ok", false, "error", error.Code)); }
            catch { return JSON.Serialize(Obj("ok", false, "error", "native_unavailable")); }
        }
    }
}
