namespace AHADesktop {
    public static partial class Native {
        [StructLayout(LayoutKind.Sequential)] struct Input { public uint Type; public InputUnion Data; }
        [StructLayout(LayoutKind.Explicit)] struct InputUnion {
            [FieldOffset(0)] public MouseInput Mouse;
            [FieldOffset(0)] public KeyboardInput Keyboard;
        }
        [StructLayout(LayoutKind.Sequential)] struct MouseInput {
            public int X, Y;
            public uint Data, Flags, Time;
            public UIntPtr Extra;
        }
        [StructLayout(LayoutKind.Sequential)] struct KeyboardInput {
            public ushort Key, Scan;
            public uint Flags, Time;
            public UIntPtr Extra;
        }
        [StructLayout(LayoutKind.Sequential)] struct Point { public int X, Y; }
        delegate IntPtr WindowProc(IntPtr hwnd, uint message, IntPtr wparam, IntPtr lparam);
        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] struct WindowClass {
            public uint Size, Style;
            public WindowProc Proc;
            public int ClassExtra, WindowExtra;
            public IntPtr Instance, Icon, Cursor, Background;
            public string Menu, Name;
            public IntPtr SmallIcon;
        }
        [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern ushort RegisterClassEx(ref WindowClass cls);
        [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern IntPtr DefWindowProc(IntPtr hwnd, uint message, IntPtr wparam, IntPtr lparam);
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode)] static extern IntPtr GetModuleHandle(string name);
        static readonly WindowProc ProbeProc = DefWindowProc;
        static bool probeClassRegistered;
        static long NativeInputRequestID = 0;
        [DllImport("user32.dll")] static extern uint SendInput(uint count, Input[] input, int size);
        [DllImport("user32.dll")] static extern bool SetForegroundWindow(IntPtr hwnd);
        [DllImport("user32.dll")] static extern bool ShowWindowAsync(IntPtr hwnd, int command);
        [DllImport("user32.dll")] static extern bool ShowWindow(IntPtr hwnd, int command);
        [DllImport("user32.dll")] static extern bool SetWindowPos(IntPtr hwnd, IntPtr after,
            int x, int y, int width, int height, uint flags);
        [DllImport("user32.dll")] static extern bool IsWindowEnabled(IntPtr hwnd);
        [DllImport("user32.dll")] static extern IntPtr GetLastActivePopup(IntPtr hwnd);
        [DllImport("user32.dll")] static extern IntPtr GetWindow(IntPtr hwnd, uint command);
        [DllImport("user32.dll")] static extern IntPtr GetTopWindow(IntPtr hwnd);
        [DllImport("user32.dll")] static extern int GetSystemMetrics(int index);
        [DllImport("user32.dll")] static extern short GetAsyncKeyState(int key);
        [DllImport("user32.dll")] static extern uint MapVirtualKey(uint code, uint mapping);
        [DllImport("user32.dll")] static extern IntPtr WindowFromPoint(Point point);
        [DllImport("user32.dll")] static extern IntPtr OpenInputDesktop(uint flags, bool inherit, uint access);
        [DllImport("user32.dll")] static extern bool CloseDesktop(IntPtr desktop);
        [DllImport("user32.dll", CharSet = CharSet.Unicode)]
        static extern bool GetUserObjectInformation(IntPtr handle, int index, StringBuilder value, int length, out int needed);
        [DllImport("user32.dll", CharSet = CharSet.Unicode)]
        static extern IntPtr CreateWindowEx(int exStyle, string cls, string title, int style,
            int x, int y, int width, int height, IntPtr parent, IntPtr menu, IntPtr instance, IntPtr param);
        [DllImport("user32.dll")] static extern bool DestroyWindow(IntPtr hwnd);
        [ComImport, Guid("A5CD92FF-29BE-454C-8D04-D82879FB3F1B"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
        interface IVirtualDesktopManager {
            [PreserveSig] int IsWindowOnCurrentVirtualDesktop(IntPtr hwnd, [MarshalAs(UnmanagedType.Bool)] out bool current);
            [PreserveSig] int GetWindowDesktopId(IntPtr hwnd, out Guid id);
            [PreserveSig] int MoveWindowToDesktop(IntPtr hwnd, ref Guid id);
        }

        static void InputDesktopGuard() {
            IntPtr desktop = OpenInputDesktop(0, false, 1);
            if (desktop == IntPtr.Zero) Fail("secure_desktop");
            try {
                int needed;
                var name = new StringBuilder(256);
                if (!GetUserObjectInformation(desktop, 2, name, name.Capacity * 2, out needed) ||
                    !name.ToString().Equals("Default", StringComparison.OrdinalIgnoreCase)) Fail("secure_desktop");
                using (var self = Process.GetCurrentProcess())
                    if (self.SessionId == 0) Fail("secure_desktop");
            } finally { CloseDesktop(desktop); }
        }
        static Target ForegroundTarget() {
            return CurrentForegroundTarget(false);
        }
        static Target CurrentForegroundTarget(bool captureOnly) {
            InputDesktopGuard();
            IntPtr hwnd = GetForegroundWindow();
            if (hwnd == IntPtr.Zero) { Fail("foreground_unavailable"); return null; }
            Target target;
            try { target = captureOnly ? InspectIdentity(hwnd) : Inspect(hwnd); }
            catch (NativeFailure error) {
                if (captureOnly && error.Code == "stale_target") Fail("foreground_unavailable");
                throw;
            }
            IntPtr process = OpenProcess(0x1000, false, target.PID);
            if (process == IntPtr.Zero) Fail("target_process_unavailable");
            try {
                IntPtr token;
                if (!OpenProcessToken(process, 8, out token)) Fail("target_token_unavailable");
                try {
                    if (Integrity(token) > 0x2000) Fail("elevated_target");
                } finally { CloseHandle(token); }
            } finally { CloseHandle(process); }
            return target;
        }
        static void NotElevated(Target target) {
            IntPtr process = OpenProcess(0x1000, false, target.PID);
            if (process == IntPtr.Zero) Fail("target_process_unavailable");
            try {
                IntPtr token;
                if (!OpenProcessToken(process, 8, out token)) Fail("target_token_unavailable");
                try { if (Integrity(token) > 0x2000) Fail("elevated_target"); }
                finally { CloseHandle(token); }
            } finally { CloseHandle(process); }
        }
        static Guid CurrentVirtualDesktop() {
            InputDesktopGuard();
            object instance = null;
            IntPtr probe = IntPtr.Zero;
            try {
                instance = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("AA509086-5CA9-4C25-8F95-589D3C07B48A")));
                var manager = (IVirtualDesktopManager)instance;
                if (!probeClassRegistered) {
                    var cls = new WindowClass { Size = (uint)Marshal.SizeOf(typeof(WindowClass)), Proc = ProbeProc,
                        Instance = GetModuleHandle(null), Name = "AHAForegroundDesktopProbe" };
                    if (RegisterClassEx(ref cls) == 0) Fail("desktop_identity_unavailable");
                    probeClassRegistered = true;
                }
                // A fresh app-class window is tracked by the shell; the built-in STATIC class is not.
                probe = CreateWindowEx(0x08040000, "AHAForegroundDesktopProbe", "AHA desktop identity probe",
                    0x00CF0000, -32000, -32000, 1, 1, IntPtr.Zero, IntPtr.Zero, GetModuleHandle(null), IntPtr.Zero);
                if (probe == IntPtr.Zero) Fail("desktop_identity_unavailable");
                ShowWindow(probe, 4);
                Guid id;
                bool current;
                for (int attempt = 0; attempt < 20; attempt++) {
                    if (manager.GetWindowDesktopId(probe, out id) == 0 && id != Guid.Empty &&
                        manager.IsWindowOnCurrentVirtualDesktop(probe, out current) == 0 && current) return id;
                    Thread.Sleep(20);
                }
                Fail("desktop_identity_unavailable"); return Guid.Empty;
            } catch (NativeFailure) { throw; }
            catch { Fail("desktop_identity_unavailable"); return Guid.Empty; }
            finally {
                if (probe != IntPtr.Zero) DestroyWindow(probe);
                if (instance != null && Marshal.IsComObject(instance)) Marshal.ReleaseComObject(instance);
            }
        }
        static Input KeyInput(ushort key, bool up, ushort scan, bool unicode) {
            bool extended = !unicode && (key == 0x5B || key >= 0x21 && key <= 0x2E);
            if (!unicode) scan = (ushort)MapVirtualKey(key, 0);
            return new Input { Type = 1, Data = new InputUnion { Keyboard = new KeyboardInput {
                Key = key, Scan = scan, Flags = (up ? 2U : 0U) | (unicode ? 4U : 0U) | (extended ? 1U : 0U)
            } } };
        }
        static Input Mouse(uint flags, int x, int y, uint data) {
            return new Input { Type = 0, Data = new InputUnion { Mouse = new MouseInput {
                X = x, Y = y, Data = data, Flags = flags
            } } };
        }
        static void IdleInput() {
            foreach (int key in new int[] { 1, 2, 4, 0x10, 0x11, 0x12, 0x5B, 0x5C })
                if ((GetAsyncKeyState(key) & 0x8000) != 0) Fail("input_busy");
        }
        static void SendBatch(List<Input> events, List<Input> releases) {
            InputDesktopGuard();
            if (events.Count == 0 || events.Count > 128) Fail("invalid_request");
            Input[] batch = events.ToArray();
            // The parent uses only these constant markers to clean up a forcibly terminated batch.
            Console.Error.Write("aha_input_begin;" + NativeInputRequestID.ToString(CultureInfo.InvariantCulture) + ";");
            Console.Error.Flush();
            try {
                uint sent = SendInput((uint)batch.Length, batch, Marshal.SizeOf(typeof(Input)));
                if (sent != batch.Length) {
                    if (sent > 0 && releases.Count > 0)
                        SendInput((uint)releases.Count, releases.ToArray(), Marshal.SizeOf(typeof(Input)));
                    Fail(sent == 0 ? "input_blocked" : "input_partial");
                }
            } finally {
                Console.Error.Write("aha_input_end;" + NativeInputRequestID.ToString(CultureInfo.InvariantCulture) + ";");
                Console.Error.Flush();
            }
        }
        static ushort KeyCode(string name) {
            if (name.Length == 1 && (name[0] >= 'A' && name[0] <= 'Z' || name[0] >= '0' && name[0] <= '9'))
                return name[0];
            switch (name) {
                case "CTRL": return 0x11; case "ALT": return 0x12; case "SHIFT": return 0x10; case "WIN": return 0x5B;
                case "ENTER": return 0x0D; case "ESC": case "ESCAPE": return 0x1B; case "TAB": return 9;
                case "SPACE": return 0x20; case "BACKSPACE": return 8; case "DELETE": return 0x2E;
                case "INSERT": return 0x2D; case "HOME": return 0x24; case "END": return 0x23;
                case "PAGEUP": return 0x21; case "PAGEDOWN": return 0x22; case "LEFT": return 0x25;
                case "UP": return 0x26; case "RIGHT": return 0x27; case "DOWN": return 0x28;
            }
            int function;
            if (name.StartsWith("F") && int.TryParse(name.Substring(1), out function) && function >= 1 && function <= 12)
                return (ushort)(0x6F + function);
            Fail("invalid_request"); return 0;
        }
        static void KeyChord(string[] names) {
            if (names.Length < 1 || names.Length > 4) Fail("invalid_request");
            var unique = new HashSet<string>(names);
            if (unique.Count != names.Length) Fail("invalid_request");
            if (unique.Contains("CTRL") && unique.Contains("ALT") && unique.Contains("DELETE") ||
                unique.Contains("WIN") && unique.Contains("CTRL") && unique.Contains("F4") ||
                unique.Contains("WIN") && (unique.Contains("L") || unique.Contains("U"))) Fail("unsafe_key_chord");
            var down = new List<Input>();
            var up = new List<Input>();
            int nonModifiers = 0;
            // Normalize modifiers before the ordinary key, independently of JSON order.
            foreach (string modifier in new string[] { "CTRL", "ALT", "SHIFT", "WIN" })
                if (unique.Contains(modifier)) down.Add(KeyInput(KeyCode(modifier), false, 0, false));
            foreach (string name in names) {
                ushort code = KeyCode(name);
                if (name == "CTRL" || name == "ALT" || name == "SHIFT" || name == "WIN") continue;
                nonModifiers++;
                down.Add(KeyInput(code, false, 0, false));
            }
            if (nonModifiers > 1) Fail("invalid_request");
            for (int i = down.Count - 1; i >= 0; i--)
                up.Add(KeyInput(down[i].Data.Keyboard.Key, true, 0, false));
            down.AddRange(up);
            IdleInput();
            SendBatch(down, up);
        }
        static Dictionary<string, object> CreateDesktop() {
            PhysicalMonitor monitor = ResolveMonitor("", true);
            Target initial = ForegroundTarget();
            Guid before = CurrentVirtualDesktop();
            if (ForegroundTarget().ID != initial.ID) Fail("foreground_changed");
            KeyChord(new string[] { "WIN", "CTRL", "D" });
            for (int attempt = 0; attempt < 30; attempt++) {
                Thread.Sleep(100);
                Guid after = CurrentVirtualDesktop();
                if (after != before)
                    return DesktopWindow(after, ResolveMonitor(monitor.ID, false));
            }
            Fail("desktop_creation_unverified"); return null;
        }
        sealed class Surface {
            public Target Target;
            public bool Desktop;
            public Rect Bounds;
            public string Identity;
            public string DesktopID;
            public Dictionary<string, object> Window;
            public PhysicalMonitor Monitor;
            public string ControlError = "";
        }
        static Target OwnedPopup(Target grant, IntPtr popup) {
            if (popup == IntPtr.Zero || popup == grant.Handle || !IsWindowVisible(popup) || !IsWindowEnabled(popup)) return null;
            uint pid;
            GetWindowThreadProcessId(popup, out pid);
            if (pid != grant.PID) return null;
            IntPtr owner = GetWindow(popup, 4); // GW_OWNER, not an unrelated foreground window.
            for (int depth = 0; depth < 16 && owner != IntPtr.Zero; depth++) {
                if (owner == grant.Handle) {
                    try {
                        Target candidate = Inspect(popup);
                        return candidate.PID == grant.PID ? candidate : null;
                    } catch { return null; }
                }
                owner = GetWindow(owner, 4);
            }
            return null;
        }
        static Target ResolveForegroundTarget(Target grant) {
            if (IsWindowEnabled(grant.Handle)) return grant;
            Target popup = OwnedPopup(grant, GetLastActivePopup(grant.Handle));
            if (popup != null) return popup;
            IntPtr window = GetTopWindow(IntPtr.Zero);
            for (int count = 0; window != IntPtr.Zero && count < 1024; count++, window = GetWindow(window, 2)) {
                popup = OwnedPopup(grant, window);
                if (popup != null) return popup;
            }
            Fail("target_disabled"); return null;
        }
        static Surface ReadSurface(string id) {
            return ReadSurface(id, false);
        }
        static Surface ReadSurface(string id, bool captureOnly) {
            InputDesktopGuard();
            Guid granted = Guid.Empty;
            bool desktop = id.StartsWith("desktop:", StringComparison.Ordinal);
            string[] scope = desktop ? id.Substring(8).Split('@') : new string[0];
            if (desktop && (scope.Length > 2 || !Guid.TryParse(scope[0], out granted) || granted == Guid.Empty)) Fail("stale_target");
            var surface = new Surface { Desktop = desktop };
            if (desktop) {
                Guid current = CurrentVirtualDesktop();
                if (current != granted) Fail("desktop_changed");
                if (scope.Length != 2 || scope[1] == "") Fail("monitor_required");
                surface.Monitor = ResolveMonitor(scope[1], false);
                surface.DesktopID = current.ToString("D");
                surface.Target = CurrentForegroundTarget(captureOnly);
                if (captureOnly) {
                    int cloaked;
                    if (!IsWindowVisible(surface.Target.Handle) ||
                        DwmGetWindowAttribute(surface.Target.Handle, 14, out cloaked, 4) != 0 || cloaked != 0)
                        surface.ControlError = "desktop_foreground_unavailable";
                }
                surface.Bounds = surface.Monitor.Bounds;
                surface.Window = DesktopWindow(granted, surface.Monitor);
            } else {
                Target grant = Resolve(id);
                surface.Target = ResolveForegroundTarget(grant);
                NotElevated(surface.Target);
                if (!GetWindowRect(surface.Target.Handle, out surface.Bounds)) Fail("stale_target");
                surface.Window = grant.Window;
            }
            Rect b = surface.Bounds;
            surface.Identity = id + "|" + surface.DesktopID + "|" + surface.Target.ID + "|" +
                b.Left + "," + b.Top + "," + b.Right + "," + b.Bottom + "|" + (IsIconic(surface.Target.Handle) ? "min" : "shown");
            return surface;
        }
        static string CaptureDesktop(Rect bounds, out string error) {
            error = "";
            int width = bounds.Right - bounds.Left, height = bounds.Bottom - bounds.Top;
            if (width <= 0 || height <= 0 || width > 8192 || height > 8192 || (long)width * height > MaxPixels) {
                error = "capture_size_limit"; return "";
            }
            try {
                using (var bitmap = new Bitmap(width, height, PixelFormat.Format32bppArgb)) {
                    using (Graphics graphics = Graphics.FromImage(bitmap))
                        graphics.CopyFromScreen(bounds.Left, bounds.Top, 0, 0, new Size(width, height), CopyPixelOperation.SourceCopy);
                    using (var stream = new MemoryStream()) {
                        bitmap.Save(stream, ImageFormat.Png);
                        if (stream.Length > 6 * 1024 * 1024) { error = "capture_size_limit"; return ""; }
                        return Convert.ToBase64String(stream.ToArray());
                    }
                }
            } catch { error = "desktop_capture_unavailable"; return ""; }
        }
        static bool ForegroundUnobscured(Surface surface) {
            if (GetForegroundWindow() != surface.Target.Handle || IsIconic(surface.Target.Handle)) return false;
            IntPtr current = GetTopWindow(IntPtr.Zero);
            for (int count = 0; current != IntPtr.Zero && count < 1024; count++, current = GetWindow(current, 2)) {
                if (current == surface.Target.Handle) return true;
                if (!IsWindowVisible(current) || IsIconic(current)) continue;
                int cloaked;
                if (DwmGetWindowAttribute(current, 14, out cloaked, 4) == 0 && cloaked != 0) continue;
                Rect other;
                if (GetWindowRect(current, out other) && other.Left < surface.Bounds.Right &&
                    other.Right > surface.Bounds.Left && other.Top < surface.Bounds.Bottom &&
                    other.Bottom > surface.Bounds.Top) return false;
            }
            return false;
        }
        static object ObserveForegroundNative(string id) {
            Surface surface = ReadSurface(id, true);
            string error;
            bool physicalWindow = !surface.Desktop && ForegroundUnobscured(surface);
            string image = surface.Desktop || physicalWindow ? CaptureDesktop(surface.Bounds, out error) :
                Capture(surface.Target, surface.Bounds, new List<object>(), out error);
            Surface after = ReadSurface(id, true);
            ReconcileCapture(surface, after, ref image, ref error);
            if (physicalWindow && !ForegroundUnobscured(after)) { image = ""; error = "window_capture_obscured"; }
            return Obj("window", surface.Window, "elements", new object[0], "image", image,
                "width", Math.Max(0L, (long)surface.Bounds.Right - surface.Bounds.Left),
                "height", Math.Max(0L, (long)surface.Bounds.Bottom - surface.Bounds.Top),
                "surface", surface.Identity, "capture_error", error,
                "control_error", surface.ControlError, "input_actions", CaptureActions(surface, image));
        }
        static void ReconcileCapture(Surface before, Surface after, ref string image, ref string error) {
            if (before.Identity != after.Identity) {
                if (before.Desktop && after.Desktop && before.DesktopID == after.DesktopID &&
                    before.Monitor.ID == after.Monitor.ID) {
                    // The monitor image is still in scope, but it cannot authorize
                    // input after the foreground window changed during capture.
                    before.ControlError = "desktop_foreground_changed";
                } else { image = ""; error = "refresh_required"; }
            }
            if (before.ControlError == "") before.ControlError = after.ControlError;
        }
        static string[] CaptureActions(Surface surface, string image) {
            if (surface.ControlError != "") return new string[0];
            return image == "" ? new string[] { "focus" } :
                new string[] { "click", "double_click", "scroll", "text", "key", "drag", "focus" };
        }
        static void Activate(Target target) {
            InputDesktopGuard();
            NotElevated(target);
            if (!IsWindowEnabled(target.Handle)) Fail("target_disabled");
            if (IsIconic(target.Handle)) {
                ShowWindowAsync(target.Handle, 9);
                for (int i = 0; i < 10 && IsIconic(target.Handle); i++) Thread.Sleep(30);
            }
            if (GetForegroundWindow() != target.Handle) SetForegroundWindow(target.Handle);
            for (int i = 0; i < 10 && GetForegroundWindow() != target.Handle; i++) Thread.Sleep(30);
            if (GetForegroundWindow() != target.Handle && !IsIconic(target.Handle)) {
                // Explicit foreground consent permits a real modifier tap. Windows
                // documents ALT as allowing a subsequent foreground switch; no
                // foreground-lock setting, input attachment or privilege is changed.
                ForegroundTarget();
                KeyChord(new string[] { "ALT" });
                // Let both modifier events reach the old input queue before
                // activation, otherwise ALT-up can put the new window in menu mode.
                Thread.Sleep(100);
                InputDesktopGuard();
                SetForegroundWindow(target.Handle);
                for (int i = 0; i < 10 && GetForegroundWindow() != target.Handle; i++) Thread.Sleep(30);
            }
            if (GetForegroundWindow() != target.Handle && !IsIconic(target.Handle))
                ActivateByCaptionClick(target);
            if (GetForegroundWindow() != target.Handle || IsIconic(target.Handle)) Fail("foreground_activation_unconfirmed");
            if (ForegroundTarget().ID != target.ID) Fail("foreground_changed");
        }
        static void ActivateByCaptionClick(Target target) {
            InputDesktopGuard();
            NotElevated(target);
            ForegroundTarget();
            IdleInput();
            // Raise without changing topmost state or weakening foreground-lock
            // policy, then use an ordinary click on a verified caption area.
            SetWindowPos(target.Handle, IntPtr.Zero, 0, 0, 0, 0, 0x0013);
            Thread.Sleep(50);
            Rect bounds;
            if (!GetWindowRect(target.Handle, out bounds)) Fail("stale_target");
            int width = bounds.Right - bounds.Left, height = bounds.Bottom - bounds.Top;
            if (width < 80 || height < 40) Fail("foreground_denied");
            foreach (int y in new int[] { 12, 20, 30, 40, 55 }) {
                foreach (int fraction in new int[] { 50, 65, 35, 80, 20 }) {
                    if (y >= height) continue;
                    Point point = new Point { X = bounds.Left + width * fraction / 100, Y = bounds.Top + y };
                    if (GetAncestor(WindowFromPoint(point), 2) != target.Handle) continue;
                    IntPtr hit;
                    int packed = unchecked(((int)(ushort)point.Y << 16) | (ushort)point.X);
                    if (SendControlTimeout(target.Handle, 0x0084, IntPtr.Zero, new IntPtr(packed), 3, 150, out hit) == IntPtr.Zero)
                        Fail("foreground_denied");
                    if (hit.ToInt64() != 2) continue; // HTCAPTION, never tabs, buttons or client content.
                    if (Resolve(target.ID).Handle != target.Handle ||
                        GetAncestor(WindowFromPoint(point), 2) != target.Handle) Fail("foreground_changed");
                    InputDesktopGuard();
                    IdleInput();
                    var release = new List<Input> { Mouse(4, 0, 0, 0) };
                    SendBatch(new List<Input> { Move(point), Mouse(2, 0, 0, 0), release[0] }, release);
                    for (int attempt = 0; attempt < 10 && GetForegroundWindow() != target.Handle; attempt++)
                        Thread.Sleep(30);
                    return;
                }
            }
            Fail("caption_unavailable");
        }
        static double Number(Dictionary<string, object> data, string name) {
            object value;
            if (!data.TryGetValue(name, out value)) return 0;
            double n;
            try { n = Convert.ToDouble(value, CultureInfo.InvariantCulture); }
            catch { Fail("invalid_request"); return 0; }
            if (double.IsNaN(n) || double.IsInfinity(n)) Fail("invalid_request");
            return n;
        }
        static Point ScreenPoint(Surface surface, double x, double y) {
            int width = surface.Bounds.Right - surface.Bounds.Left, height = surface.Bounds.Bottom - surface.Bounds.Top;
            if (x < 0 || y < 0 || x >= width || y >= height) Fail("invalid_request");
            return new Point { X = surface.Bounds.Left + (int)x, Y = surface.Bounds.Top + (int)y };
        }
        static Input Move(Point point) {
            int x = GetSystemMetrics(76), y = GetSystemMetrics(77), width = GetSystemMetrics(78), height = GetSystemMetrics(79);
            if (width < 2 || height < 2 || point.X < x || point.Y < y || point.X >= x + width || point.Y >= y + height)
                Fail("refresh_required");
            return Mouse(0xC001, (int)Math.Round((point.X - x) * 65535.0 / (width - 1)),
                (int)Math.Round((point.Y - y) * 65535.0 / (height - 1)), 0);
        }
        static void CheckPoint(Surface surface, Point point) {
            IntPtr root = GetAncestor(WindowFromPoint(point), 2);
            if (root == IntPtr.Zero) Fail("point_obscured");
            if (!surface.Desktop && root != surface.Target.Handle) Fail("point_obscured");
            NotElevated(Inspect(root));
        }
        static void CheckForeground(Surface surface, string id) {
            if (ReadSurface(id).Identity != surface.Identity) Fail("refresh_required");
            Target actual = ForegroundTarget();
            if (actual.ID != surface.Target.ID) Fail("foreground_changed");
        }
        static void ForegroundAction(Dictionary<string, object> request) {
            string id = Text(request, "window_id");
            var action = request["action"] as Dictionary<string, object>;
            if (action == null || Text(action, "element_id") != "$surface") Fail("invalid_request");
            string kind = Text(action, "kind");
            if (kind != "focus" && kind != "click" && kind != "double_click" && kind != "drag" &&
                kind != "scroll" && kind != "text" && kind != "key") Fail("unsupported_action");
            Surface surface = ReadSurface(id);
            if (kind == "focus") {
                if (!surface.Desktop) Activate(surface.Target);
                return;
            }
            if (surface.Identity != Text(request, "surface") ||
                Number(request, "width") != surface.Bounds.Right - surface.Bounds.Left ||
                Number(request, "height") != surface.Bounds.Bottom - surface.Bounds.Top) Fail("refresh_required");
            if (!surface.Desktop) Activate(surface.Target);
            CheckForeground(surface, id);
            IdleInput();
            if (kind == "key") {
                MonitorKeyboardGuard(surface);
                var keys = action.ContainsKey("keys") ? action["keys"] as System.Collections.IList : null;
                if (keys == null) Fail("invalid_request");
                string[] names = new string[keys.Count];
                for (int i = 0; i < keys.Count; i++) names[i] = keys[i] as string ?? "";
                KeyChord(names);
                return;
            }
            if (kind == "text") {
                string text = Text(action, "value");
                if (text.Length == 0 || Encoding.UTF8.GetByteCount(text) > 4096) Fail("invalid_request");
                foreach (char c in text)
                    if (c < 32 && c != '\r' && c != '\n' && c != '\t' || c == 127) Fail("invalid_request");
                text = text.Replace("\r\n", "\n").Replace("\r", "\n");
                for (int offset = 0; offset < text.Length;) {
                    CheckForeground(surface, id);
                    MonitorKeyboardGuard(surface);
                    IdleInput();
                    var batch = new List<Input>();
                    var releases = new List<Input>();
                    if (text[offset] == '\n' || text[offset] == '\t') {
                        ushort key = text[offset++] == '\n' ? (ushort)13 : (ushort)9;
                        batch.Add(KeyInput(key, false, 0, false));
                        Input controlKeyUp = KeyInput(key, true, 0, false);
                        batch.Add(controlKeyUp); releases.Add(controlKeyUp);
                        SendBatch(batch, releases);
                        // Let a navigation/submit key settle before validating the next text segment.
                        Thread.Sleep(20);
                        continue;
                    }
                    int end = Math.Min(offset + 32, text.Length);
                    if (end < text.Length && char.IsHighSurrogate(text[end - 1])) end--;
                    for (; offset < end; offset++) {
                        if (text[offset] == '\n' || text[offset] == '\t') break;
                        batch.Add(KeyInput(0, false, text[offset], true));
                        Input up = KeyInput(0, true, text[offset], true);
                        batch.Add(up); releases.Add(up);
                    }
                    SendBatch(batch, releases);
                }
                return;
            }
            Point start = ScreenPoint(surface, Number(action, "x"), Number(action, "y"));
            CheckPoint(surface, start);
            var inputs = new List<Input> { Move(start) };
            var release = new List<Input>();
            if (kind == "scroll") {
                double dx = Number(action, "delta_x"), dy = Number(action, "delta_y");
                if (Math.Abs(dx) > 2400 || Math.Abs(dy) > 2400 || dx == 0 && dy == 0) Fail("invalid_request");
                if (dy != 0) inputs.Add(Mouse(0x0800, 0, 0, unchecked((uint)(int)-Math.Round(dy))));
                if (dx != 0) inputs.Add(Mouse(0x1000, 0, 0, unchecked((uint)(int)Math.Round(dx))));
            } else {
                string button = Text(action, "button");
                uint down = 2, up = 4;
                if (button == "right") { down = 8; up = 16; }
                else if (button == "middle") { down = 32; up = 64; }
                else if (button != "" && button != "left") Fail("invalid_request");
                inputs.Add(Mouse(down, 0, 0, 0));
                if (kind == "drag") {
                    Point end = ScreenPoint(surface, Number(action, "end_x"), Number(action, "end_y"));
                    for (int i = 1; i <= 12; i++) {
                        Point point = new Point { X = start.X + (end.X - start.X) * i / 12, Y = start.Y + (end.Y - start.Y) * i / 12 };
                        CheckPoint(surface, point);
                        Input move = Move(point);
                        move.Data.Mouse.Flags |= 0x2000;
                        inputs.Add(move);
                    }
                }
                inputs.Add(Mouse(up, 0, 0, 0));
                release.Add(Mouse(up, 0, 0, 0));
                if (kind == "double_click") { inputs.Add(Mouse(down, 0, 0, 0)); inputs.Add(Mouse(up, 0, 0, 0)); }
            }
            CheckForeground(surface, id);
            SendBatch(inputs, release);
        }
        public static string RunForeground(string requestJSON) {
            try {
                if (SetThreadDpiAwarenessContext(new IntPtr(-4)) == IntPtr.Zero) Fail("native_unavailable");
                var request = JSON.Deserialize<Dictionary<string, object>>(requestJSON);
                string operation = Text(request, "operation");
                if (operation == "foreground_new_desktop") return JSON.Serialize(Obj("ok", true, "window", CreateDesktop()));
                if (operation == "foreground_observe") return JSON.Serialize(Obj("ok", true, "observation", ObserveForegroundNative(Text(request, "window_id"))));
                if (operation == "foreground_act") { ForegroundAction(request); return JSON.Serialize(Obj("ok", true)); }
                Fail("invalid_request");
            } catch (NativeFailure ex) { return JSON.Serialize(Obj("ok", false, "error", ex.Code)); }
            catch { return JSON.Serialize(Obj("ok", false, "error", "native_unavailable")); }
            return JSON.Serialize(Obj("ok", false, "error", "invalid_request"));
        }
    }
}
