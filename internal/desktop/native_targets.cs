namespace AHADesktop {
    public static partial class Native {
        delegate bool MonitorProc(IntPtr monitor, IntPtr dc, ref Rect rect, IntPtr data);
        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] struct MonitorInfo {
            public uint Size;
            public Rect Monitor, Work;
            public uint Flags;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)] public string Device;
        }
        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] struct DisplayDevice {
            public uint Size;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)] public string Name;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string Description;
            public uint Flags;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string ID;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string Key;
        }
        [DllImport("user32.dll")] static extern bool EnumDisplayMonitors(IntPtr dc, IntPtr clip, MonitorProc callback, IntPtr data);
        [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetMonitorInfoW")]
        static extern bool GetMonitorInfo(IntPtr monitor, ref MonitorInfo info);
        [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "EnumDisplayDevicesW")]
        static extern bool EnumDisplayDevices(string device, uint index, ref DisplayDevice info, uint flags);
        [DllImport("user32.dll")] static extern IntPtr MonitorFromWindow(IntPtr hwnd, uint flags);
        sealed class PhysicalMonitor {
            public string ID, Name;
            public IntPtr Handle;
            public Rect Bounds;
            public bool Primary;
            public Dictionary<string, object> Describe() {
                return Obj("id", ID, "name", Name, "x", Bounds.Left, "y", Bounds.Top,
                    "width", Bounds.Right - Bounds.Left, "height", Bounds.Bottom - Bounds.Top, "primary", Primary);
            }
        }
        static string MonitorIdentity(string device, string hardware, IntPtr handle, Rect rect) {
            string value = device + "|" + hardware + "|" + handle.ToInt64().ToString("x", CultureInfo.InvariantCulture) +
                "|" + rect.Left + "," + rect.Top + "," + rect.Right + "," + rect.Bottom;
            using (var sha = System.Security.Cryptography.SHA256.Create()) {
                byte[] hash = sha.ComputeHash(Encoding.UTF8.GetBytes(value));
                return BitConverter.ToString(hash, 0, 16).Replace("-", "").ToLowerInvariant();
            }
        }
        static List<PhysicalMonitor> PhysicalMonitors() {
            InputDesktopGuard();
            var result = new List<PhysicalMonitor>();
            bool failed = false;
            bool enumerated = EnumDisplayMonitors(IntPtr.Zero, IntPtr.Zero,
                delegate(IntPtr handle, IntPtr dc, ref Rect unused, IntPtr data) {
                    if (result.Count >= 32) { failed = true; return false; }
                    var info = new MonitorInfo { Size = (uint)Marshal.SizeOf(typeof(MonitorInfo)) };
                    if (!GetMonitorInfo(handle, ref info) || info.Monitor.Right <= info.Monitor.Left ||
                        info.Monitor.Bottom <= info.Monitor.Top) { failed = true; return false; }
                    string hardware = "", description = "";
                    for (uint i = 0; i < 32; i++) {
                        var display = new DisplayDevice { Size = (uint)Marshal.SizeOf(typeof(DisplayDevice)) };
                        if (!EnumDisplayDevices(info.Device, i, ref display, 1)) break;
                        if ((display.Flags & 1) == 0 || string.IsNullOrEmpty(display.ID)) continue;
                        hardware += "|" + display.ID;
                        if (description == "") description = display.Description;
                    }
                    if (hardware == "") { failed = true; return false; }
                    result.Add(new PhysicalMonitor { Handle = handle, Bounds = info.Monitor, Primary = (info.Flags & 1) != 0,
                        ID = MonitorIdentity(info.Device, hardware, handle, info.Monitor),
                        Name = Bounded(info.Device + (description == "" ? "" : " - " + description)) });
                    return true;
                }, IntPtr.Zero);
            if (!enumerated || failed || result.Count == 0) Fail("monitor_unavailable");
            return result;
        }
        static PhysicalMonitor ResolveMonitor(string id, bool allowDefault) {
            foreach (PhysicalMonitor monitor in PhysicalMonitors())
                if (monitor.ID == id || allowDefault && id == "" && monitor.Primary) return monitor;
            Fail(id == "" ? "monitor_required" : "monitor_changed"); return null;
        }
        static Dictionary<string, object> DesktopWindow(Guid desktop, PhysicalMonitor monitor) {
            return Obj("id", "desktop:" + desktop.ToString("D") + "@" + monitor.ID,
                "desktop_id", desktop.ToString("D"), "monitor_id", monitor.ID,
                "kind", "desktop", "title", "Windows desktop - " + monitor.Name, "process", "Windows");
        }
        static bool ContainsPoint(Rect bounds, int x, int y) {
            return x >= bounds.Left && x < bounds.Right && y >= bounds.Top && y < bounds.Bottom;
        }
        static void MonitorKeyboardGuard(Surface surface) {
            if (!surface.Desktop) return;
            if (surface.Monitor == null || MonitorFromWindow(surface.Target.Handle, 0) != surface.Monitor.Handle)
                Fail("foreground_outside_monitor");
            GUIInfo info = new GUIInfo { Size = (uint)Marshal.SizeOf(typeof(GUIInfo)) };
            if (!GetGUIThreadInfo(surface.Target.Thread, ref info)) Fail("focus_unverifiable");
            if (info.Focus != IntPtr.Zero) {
                Rect focus;
                if (!GetWindowRect(info.Focus, out focus) || !ContainsPoint(surface.Bounds,
                    (int)(((long)focus.Left + focus.Right) / 2), (int)(((long)focus.Top + focus.Bottom) / 2)))
                    Fail("foreground_outside_monitor");
            }
        }
        const string DesktopRegistryPath = @"Software\Microsoft\Windows\CurrentVersion\Explorer\VirtualDesktops";
        static List<Guid> ReadDesktopOrder() {
            // Explorer's registry layout is undocumented. Never trust it without a
            // public COM current-GUID check, and never write this state.
            using (var key = Microsoft.Win32.Registry.CurrentUser.OpenSubKey(DesktopRegistryPath, false)) {
                byte[] bytes = key == null ? null : key.GetValue("VirtualDesktopIDs", null) as byte[];
                if (bytes == null || bytes.Length == 0 || bytes.Length % 16 != 0 || bytes.Length > 64 * 16)
                    Fail("desktop_enumeration_unavailable");
                var result = new List<Guid>();
                for (int offset = 0; offset < bytes.Length; offset += 16) {
                    var raw = new byte[16];
                    Array.Copy(bytes, offset, raw, 0, 16);
                    Guid id = new Guid(raw);
                    if (id == Guid.Empty || result.Contains(id)) Fail("desktop_state_inconsistent");
                    result.Add(id);
                }
                return result;
            }
        }
        static bool SameOrder(List<Guid> first, List<Guid> second) {
            if (first.Count != second.Count) return false;
            for (int i = 0; i < first.Count; i++) if (first[i] != second[i]) return false;
            return true;
        }
        static List<Guid> VerifiedDesktopOrder(out Guid current) {
            var before = ReadDesktopOrder();
            current = CurrentVirtualDesktop();
            var after = ReadDesktopOrder();
            if (!before.Contains(current) || !SameOrder(before, after)) Fail("desktop_state_inconsistent");
            return before;
        }
        static string DesktopName(Guid id, int index) {
            try {
                using (var key = Microsoft.Win32.Registry.CurrentUser.OpenSubKey(DesktopRegistryPath + @"\" + id.ToString("B"), false)) {
                    string name = key == null ? null : key.GetValue("Name", null) as string;
                    if (!string.IsNullOrWhiteSpace(name)) return Bounded(name);
                }
            } catch { }
            return "Desktop " + (index + 1).ToString(CultureInfo.InvariantCulture);
        }
        static bool TryWindowDesktop(IntPtr hwnd, out Guid desktop, out bool current) {
            desktop = Guid.Empty;
            current = false;
            object instance = null;
            try {
                instance = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("AA509086-5CA9-4C25-8F95-589D3C07B48A")));
                var manager = (IVirtualDesktopManager)instance;
                return manager.IsWindowOnCurrentVirtualDesktop(hwnd, out current) == 0 &&
                    manager.GetWindowDesktopId(hwnd, out desktop) == 0 && desktop != Guid.Empty;
            } catch { return false; }
            finally { if (instance != null && Marshal.IsComObject(instance)) Marshal.ReleaseComObject(instance); }
        }
        static void DescribeWindowDesktop(Target target, Guid desktop, List<Guid> known, bool other) {
            target.Window["kind"] = "window";
            if (desktop == Guid.Empty) return;
            target.Window["desktop_id"] = desktop.ToString("D");
            int index = known.IndexOf(desktop);
            if (index >= 0) target.Window["desktop_name"] = DesktopName(desktop, index);
            target.Window["other_desktop"] = other;
        }
        static Target InspectTargetWindow(IntPtr hwnd, Guid active, List<Guid> known) {
            Guid desktop;
            bool current;
            if (!TryWindowDesktop(hwnd, out desktop, out current)) {
                Target local = InspectPickerWindow(hwnd);
                local.Window["kind"] = "window";
                return local;
            }
            bool other = !current;
            if (other && (active == Guid.Empty || desktop == active || !known.Contains(desktop)))
                Fail("desktop_identity_unavailable");
            // DWM_CLOAKED_SHELL is expected only for a verified other desktop.
            // Application/inherited cloaking is never accepted for listing.
            Target target = InspectPickerWindow(hwnd, other ? 2 : 0);
            Guid after;
            bool stillCurrent;
            if (!TryWindowDesktop(hwnd, out after, out stillCurrent) || after != desktop || stillCurrent != current)
                Fail("desktop_changed");
            DescribeWindowDesktop(target, current ? active : desktop, known, other);
            return target;
        }
        static object EnumerateTargets() {
            var monitors = new List<object>();
            foreach (var monitor in PhysicalMonitors()) monitors.Add(monitor.Describe());
            var desktops = new List<object>();
            string reason = "";
            bool switching = false;
            Guid active = Guid.Empty;
            var known = new List<Guid>();
            try {
                Guid current;
                List<Guid> ids = VerifiedDesktopOrder(out current);
                active = current;
                known = ids;
                for (int i = 0; i < ids.Count; i++)
                    desktops.Add(Obj("id", ids[i].ToString("D"), "name", DesktopName(ids[i], i), "current", ids[i] == current));
                switching = true;
            } catch (NativeFailure error) {
                reason = error.Code;
                try {
                    Guid current = CurrentVirtualDesktop();
                    active = current;
                    known.Add(current);
                    desktops.Add(Obj("id", current.ToString("D"), "name", "Current desktop", "current", true));
                } catch { }
            } catch { reason = "desktop_enumeration_unavailable"; }
            var windows = new List<object>();
            EnumWindows(delegate(IntPtr hwnd, IntPtr unused) {
                if (windows.Count >= 512) return false;
                try {
                    var window = InspectTargetWindow(hwnd, active, known).Window;
                    window["kind"] = "window";
                    windows.Add(window);
                } catch { }
                return true;
            }, IntPtr.Zero);
            return Obj("windows", windows, "monitors", monitors, "desktops", desktops,
                "desktop_switch_supported", switching, "desktop_reason", reason);
        }
        static void SwitchVirtualDesktop(Guid selected) {
            if (CurrentVirtualDesktop() == selected) return;
            Guid current;
            List<Guid> initial = VerifiedDesktopOrder(out current);
            int currentIndex = initial.IndexOf(current), targetIndex = initial.IndexOf(selected);
            if (targetIndex < 0) Fail("stale_target");
            if (Math.Abs(targetIndex - currentIndex) > 16) Fail("desktop_switch_limit");
            while (current != selected) {
                Guid verified;
                List<Guid> order = VerifiedDesktopOrder(out verified);
                if (!SameOrder(initial, order) || verified != current) Fail("desktop_state_inconsistent");
                int direction = targetIndex > currentIndex ? 1 : -1;
                Guid expected = initial[currentIndex + direction];
                ForegroundTarget();
                KeyChord(new string[] { "WIN", "CTRL", direction > 0 ? "RIGHT" : "LEFT" });
                bool changed = false;
                for (int i = 0; i < 30; i++) {
                    Thread.Sleep(50);
                    Guid after = CurrentVirtualDesktop();
                    if (after == current) continue;
                    if (after != expected || !SameOrder(initial, ReadDesktopOrder())) Fail("desktop_state_inconsistent");
                    current = after;
                    currentIndex += direction;
                    changed = true;
                    break;
                }
                if (!changed) Fail("desktop_switch_unverified");
            }
        }
        static object SelectNativeTarget(Dictionary<string, object> selection) {
            string kind = Text(selection, "kind");
            string window = Text(selection, "window_id"), desktop = Text(selection, "desktop_id"), monitorID = Text(selection, "monitor_id");
            if (kind == "window") {
                if (window == "" || desktop != "" || monitorID != "") Fail("invalid_request");
                IntPtr hwnd = WindowHandle(window);
                Guid belonging;
                bool current;
                bool located = TryWindowDesktop(hwnd, out belonging, out current);
                if (!located || current) {
                    Target local = Resolve(window);
                    Guid after;
                    bool stillCurrent;
                    if (located && (!TryWindowDesktop(hwnd, out after, out stillCurrent) || !stillCurrent))
                        Fail("desktop_changed");
                    local.Window["kind"] = "window";
                    return local.Window;
                }
                Guid active;
                List<Guid> known = VerifiedDesktopOrder(out active);
                Target candidate = InspectTargetWindow(hwnd, active, known);
                if (candidate.ID != window || !known.Contains(belonging)) Fail("stale_target");
                if (Text(candidate.Window, "desktop_id") != belonging.ToString("D") ||
                    CurrentVirtualDesktop() != active) Fail("desktop_changed");
                SwitchVirtualDesktop(belonging);
                for (int attempt = 0; attempt < 20; attempt++) {
                    if (CurrentVirtualDesktop() != belonging) Fail("desktop_changed");
                    Guid after;
                    bool nowCurrent;
                    if (!TryWindowDesktop(hwnd, out after, out nowCurrent) || after != belonging)
                        Fail("stale_target");
                    if (nowCurrent) {
                        try {
                            Target selected = InspectPickerWindow(hwnd);
                            if (selected.ID != window) Fail("stale_target");
                            DescribeWindowDesktop(selected, belonging, known, false);
                            return selected.Window;
                        } catch (NativeFailure error) {
                            if (error.Code != "unsupported_target" && error.Code != "stale_target") throw;
                        }
                    }
                    Thread.Sleep(50);
                }
                Fail("desktop_switch_unverified"); return null;
            }
            if (window != "" || (kind != "desktop" && kind != "new-desktop")) Fail("invalid_request");
            PhysicalMonitor monitor = ResolveMonitor(monitorID, true);
            Guid id;
            if (kind == "new-desktop") {
                if (desktop != "") Fail("invalid_request");
                var created = CreateDesktop();
                id = Guid.Parse((string)created["desktop_id"]);
            } else {
                if (!Guid.TryParse(desktop, out id) || id == Guid.Empty) Fail("invalid_request");
                SwitchVirtualDesktop(id);
            }
            if (CurrentVirtualDesktop() != id) Fail("desktop_changed");
            monitor = ResolveMonitor(monitor.ID, false);
            return DesktopWindow(id, monitor);
        }
        public static string RunTargets(string requestJSON) {
            try {
                if (SetThreadDpiAwarenessContext(new IntPtr(-4)) == IntPtr.Zero) Fail("native_unavailable");
                InputDesktopGuard();
                var request = JSON.Deserialize<Dictionary<string, object>>(requestJSON);
                string operation = Text(request, "operation");
                if (operation == "targets") return JSON.Serialize(Obj("ok", true, "targets", EnumerateTargets()));
                if (operation == "target_select") {
                    var selection = request["selection"] as Dictionary<string, object>;
                    if (selection == null) Fail("invalid_request");
                    return JSON.Serialize(Obj("ok", true, "window", SelectNativeTarget(selection)));
                }
                Fail("invalid_request");
            } catch (NativeFailure error) { return JSON.Serialize(Obj("ok", false, "error", error.Code)); }
            catch { return JSON.Serialize(Obj("ok", false, "error", "native_unavailable")); }
            return JSON.Serialize(Obj("ok", false, "error", "invalid_request"));
        }
    }
}
