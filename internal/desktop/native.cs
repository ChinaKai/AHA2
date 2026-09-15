using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.Drawing.Imaging;
using System.Globalization;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.Principal;
using System.Text;
using System.Threading;
using System.Web.Script.Serialization;
using System.Windows.Automation;

namespace AHADesktop {
    public static partial class Native {
        const int MaxElements = 512;
        const int MaxNodes = 4096;
        const long MaxPixels = 12000000;
        static readonly JavaScriptSerializer JSON = new JavaScriptSerializer { MaxJsonLength = 12 * 1024 * 1024 };
        delegate bool EnumProc(IntPtr hwnd, IntPtr param);
        [StructLayout(LayoutKind.Sequential)] struct Rect { public int Left, Top, Right, Bottom; }
        [StructLayout(LayoutKind.Sequential)] struct PlacementPoint { public int X, Y; }
        [StructLayout(LayoutKind.Sequential)] struct WindowPlacement {
            public uint Length, Flags, Show;
            public PlacementPoint MinPosition, MaxPosition;
            public Rect NormalPosition;
        }
        [StructLayout(LayoutKind.Sequential)] struct Times { public uint Low, High; }
        [StructLayout(LayoutKind.Sequential)] struct GUIInfo {
            public uint Size, Flags;
            public IntPtr Active, Focus, Capture, MenuOwner, MoveSize, Caret;
            public Rect CaretRect;
        }
        [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc callback, IntPtr param);
        [DllImport("user32.dll")] static extern bool EnumChildWindows(IntPtr parent, EnumProc callback, IntPtr param);
        [DllImport("user32.dll")] static extern bool IsWindow(IntPtr hwnd);
        [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr hwnd);
        [DllImport("user32.dll")] static extern bool IsIconic(IntPtr hwnd);
        [DllImport("user32.dll")] static extern IntPtr GetAncestor(IntPtr hwnd, uint flags);
        [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint pid);
        [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetWindowText(IntPtr hwnd, StringBuilder text, int max);
        [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetClassName(IntPtr hwnd, StringBuilder text, int max);
        [DllImport("user32.dll", EntryPoint = "GetWindowLongW")] static extern int GetWindowLong(IntPtr hwnd, int index);
        [DllImport("user32.dll", EntryPoint = "SendMessageTimeoutW", CharSet = CharSet.Unicode)]
        static extern IntPtr SendTextTimeout(IntPtr hwnd, uint message, IntPtr wParam, string value, uint flags, uint timeout, out IntPtr result);
        [DllImport("user32.dll", EntryPoint = "SendMessageTimeoutW")]
        static extern IntPtr SendControlTimeout(IntPtr hwnd, uint message, IntPtr wParam, IntPtr lParam, uint flags, uint timeout, out IntPtr result);
        [DllImport("user32.dll")] static extern IntPtr GetParent(IntPtr hwnd);
        [DllImport("user32.dll")] static extern int GetDlgCtrlID(IntPtr hwnd);
        [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr hwnd, out Rect rect);
        [DllImport("user32.dll")] static extern bool GetClientRect(IntPtr hwnd, out Rect rect);
        [DllImport("user32.dll")] static extern bool GetWindowPlacement(IntPtr hwnd, ref WindowPlacement placement);
        [DllImport("user32.dll")] static extern IntPtr GetShellWindow();
        [DllImport("user32.dll")] static extern bool PrintWindow(IntPtr hwnd, IntPtr dc, uint flags);
        [DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();
        [DllImport("user32.dll")] static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
        [DllImport("user32.dll")] static extern bool GetGUIThreadInfo(uint thread, ref GUIInfo info);
        [DllImport("kernel32.dll")] static extern IntPtr OpenProcess(uint access, bool inherit, uint pid);
        [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
        [DllImport("kernel32.dll")] static extern bool GetProcessTimes(IntPtr process, out Times created, out Times exited, out Times kernel, out Times user);
        [DllImport("advapi32.dll")] static extern bool OpenProcessToken(IntPtr process, uint access, out IntPtr token);
        [DllImport("advapi32.dll")] static extern bool GetTokenInformation(IntPtr token, int infoClass, IntPtr data, int length, out int returned);
        [DllImport("advapi32.dll")] static extern IntPtr GetSidSubAuthorityCount(IntPtr sid);
        [DllImport("advapi32.dll")] static extern IntPtr GetSidSubAuthority(IntPtr sid, uint index);
        [DllImport("dwmapi.dll")] static extern int DwmGetWindowAttribute(IntPtr hwnd, int attr, out int value, int size);

        sealed class NativeFailure : Exception {
            public readonly string Code;
            public NativeFailure(string code) { Code = code; }
        }
        static void Fail(string code) { throw new NativeFailure(code); }
        static Dictionary<string, object> Obj(params object[] args) {
            var result = new Dictionary<string, object>();
            for (int i = 0; i < args.Length; i += 2) result[(string)args[i]] = args[i + 1];
            return result;
        }
        static string Text(Dictionary<string, object> d, string key) {
            object value;
            return d.TryGetValue(key, out value) ? value as string ?? "" : "";
        }
        static string Bounded(string value) {
            if (value == null) return "";
            return value.Length <= 2048 ? value : value.Substring(0, 2048);
        }
        static string TokenSID(IntPtr token, int infoClass) {
            int size;
            GetTokenInformation(token, infoClass, IntPtr.Zero, 0, out size);
            if (size < IntPtr.Size || size > 65536) Fail("unsupported_target");
            IntPtr data = Marshal.AllocHGlobal(size);
            try {
                if (!GetTokenInformation(token, infoClass, data, size, out size)) Fail("unsupported_target");
                return new SecurityIdentifier(Marshal.ReadIntPtr(data)).Value;
            } finally { Marshal.FreeHGlobal(data); }
        }
        static int Integrity(IntPtr token) {
            int size;
            GetTokenInformation(token, 25, IntPtr.Zero, 0, out size);
            if (size < IntPtr.Size || size > 65536) Fail("unsupported_target");
            IntPtr data = Marshal.AllocHGlobal(size);
            try {
                if (!GetTokenInformation(token, 25, data, size, out size)) Fail("unsupported_target");
                IntPtr sid = Marshal.ReadIntPtr(data);
                byte count = Marshal.ReadByte(GetSidSubAuthorityCount(sid));
                if (count == 0) Fail("unsupported_target");
                return Marshal.ReadInt32(GetSidSubAuthority(sid, (uint)(count - 1)));
            } finally { Marshal.FreeHGlobal(data); }
        }
        sealed class Target {
            public IntPtr Handle;
            public uint PID;
            public uint Thread;
            public string ID;
            public Dictionary<string, object> Window;
            public bool IncludeOffscreen = false;
            public System.Action Revalidate = null;
        }
        static Target Inspect(IntPtr hwnd) {
            return InspectVisualWindow(hwnd, 0);
        }
        static Target InspectVisualWindow(IntPtr hwnd, int allowedCloaking) {
            if (!IsWindow(hwnd) || !IsWindowVisible(hwnd) || GetAncestor(hwnd, 2) != hwnd) Fail("stale_target");
            int cloaked;
            if (DwmGetWindowAttribute(hwnd, 14, out cloaked, 4) != 0) Fail("target_state_unavailable");
            if ((cloaked & ~allowedCloaking) != 0) Fail("unsupported_target");
            return InspectIdentity(hwnd);
        }
        static Target InspectIdentity(IntPtr hwnd) {
            if (!IsWindow(hwnd) || GetAncestor(hwnd, 2) != hwnd) Fail("stale_target");
            return InspectWindowProcessIdentity(hwnd, true);
        }
        static Target InspectWindowProcessIdentity(IntPtr hwnd, bool describeWindow) {
            if (!IsWindow(hwnd)) Fail("stale_target");
            uint pid;
            uint thread = GetWindowThreadProcessId(hwnd, out pid);
            if (pid == 0 || thread == 0) Fail("stale_target");
            using (Process process = Process.GetProcessById((int)pid))
            using (Process self = Process.GetCurrentProcess()) {
                if (process.SessionId != self.SessionId || self.SessionId == 0) Fail("target_foreign_session");
                IntPtr handle = OpenProcess(0x1000, false, pid);
                if (handle == IntPtr.Zero) Fail("target_process_unavailable");
                try {
                    Times created, exited, kernel, user;
                    if (!GetProcessTimes(handle, out created, out exited, out kernel, out user)) Fail("stale_target");
                    IntPtr token;
                    if (!OpenProcessToken(handle, 8, out token)) Fail("target_token_unavailable");
                    try {
                        using (WindowsIdentity identity = WindowsIdentity.GetCurrent()) {
                            string owner;
                            int targetIntegrity, ownIntegrity;
                            try {
                                owner = TokenSID(token, 1);
                                targetIntegrity = Integrity(token);
                                ownIntegrity = Integrity(identity.Token);
                            } catch (NativeFailure) {
                                Fail("target_token_unavailable"); return null;
                            }
                            if (owner != identity.User.Value) Fail("target_foreign_user");
                            if (targetIntegrity > ownIntegrity) Fail("elevated_target");
                        }
                    } finally { CloseHandle(token); }
                    ulong birth = ((ulong)created.High << 32) | created.Low;
                    string id = pid.ToString("x", CultureInfo.InvariantCulture) + "." +
                        birth.ToString("x", CultureInfo.InvariantCulture) + "." + hwnd.ToInt64().ToString("x", CultureInfo.InvariantCulture);
                    var title = new StringBuilder(2049);
                    if (describeWindow) GetWindowText(hwnd, title, title.Capacity);
                    return new Target { Handle = hwnd, PID = pid, Thread = thread, ID = id,
                        Window = Obj("id", id, "title", title.ToString(), "process", Bounded(process.ProcessName)) };
                } finally { CloseHandle(handle); }
            }
        }
        // Picker eligibility is deliberately separate from an existing grant's
        // identity checks: minimizing or changing styles must not revoke a grant.
        static Target InspectPickerWindow(IntPtr hwnd) {
            return InspectPickerWindow(hwnd, 0);
        }
        static Target InspectPickerWindow(IntPtr hwnd, int allowedCloaking) {
            Target target = InspectVisualWindow(hwnd, allowedCloaking);
            if (hwnd == GetShellWindow()) Fail("unsupported_target");
            int extended = GetWindowLong(hwnd, -20);
            const int ToolWindow = 0x00000080, NoActivate = 0x08000000, AppWindow = 0x00040000;
            if ((extended & (ToolWindow | NoActivate)) != 0 && (extended & AppWindow) == 0)
                Fail("unsupported_target");
            Rect bounds;
            if (IsIconic(hwnd)) {
                var placement = new WindowPlacement();
                placement.Length = (uint)Marshal.SizeOf(typeof(WindowPlacement));
                if (!GetWindowPlacement(hwnd, ref placement)) Fail("unsupported_target");
                bounds = placement.NormalPosition;
            } else if (!GetClientRect(hwnd, out bounds)) {
                Fail("unsupported_target");
                return null;
            }
            if (bounds.Right <= bounds.Left || bounds.Bottom <= bounds.Top) Fail("unsupported_target");
            return target;
        }
        static IntPtr WindowHandle(string id) {
            string[] parts = id.Split('.');
            long hwnd;
            if (parts.Length != 3 || id.Length > 128 ||
                !long.TryParse(parts[2], NumberStyles.AllowHexSpecifier, CultureInfo.InvariantCulture, out hwnd)) {
                Fail("stale_target"); return IntPtr.Zero;
            }
            return new IntPtr(hwnd);
        }
        static Target Resolve(string id) {
            Target target = Inspect(WindowHandle(id));
            if (target.ID != id) Fail("stale_target");
            return target;
        }
        static void RevalidateTarget(Target target) {
            if (target.Revalidate != null) target.Revalidate();
            else Resolve(target.ID);
        }
        sealed class Focus {
            public IntPtr Foreground, Active, Focused, TargetFocused, TargetActive;
            public uint TargetThread;
            public static Focus Read(uint targetThread) {
                IntPtr foreground = GetForegroundWindow();
                if (foreground == IntPtr.Zero) { Fail("focus_unverifiable"); return null; }
                uint pid;
                uint thread = GetWindowThreadProcessId(foreground, out pid);
                GUIInfo info = new GUIInfo { Size = (uint)Marshal.SizeOf(typeof(GUIInfo)) };
                if (thread == 0 || !GetGUIThreadInfo(thread, ref info)) Fail("focus_unverifiable");
                GUIInfo target = new GUIInfo { Size = (uint)Marshal.SizeOf(typeof(GUIInfo)) };
                if (!GetGUIThreadInfo(targetThread, ref target)) Fail("focus_unverifiable");
                return new Focus { Foreground = foreground, Active = info.Active, Focused = info.Focus,
                    TargetThread = targetThread, TargetFocused = target.Focus, TargetActive = target.Active };
            }
            public void Check() {
                Focus current = Read(TargetThread);
                if (Foreground != current.Foreground || Active != current.Active || Focused != current.Focused ||
                    TargetFocused != current.TargetFocused || TargetActive != current.TargetActive)
                    Fail("focus_side_effect");
            }
        }
        static string ElementID(AutomationElement element) {
            int[] runtime = element.GetRuntimeId();
            if (runtime == null || runtime.Length == 0 || runtime.Length > 64) Fail("element_unavailable");
            return string.Join(".", Array.ConvertAll(runtime, x => x.ToString(CultureInfo.InvariantCulture)));
        }
        static string NativeClass(AutomationElement element) {
            int handle = element.Current.NativeWindowHandle;
            if (handle == 0) return "";
            var name = new StringBuilder(256);
            if (GetClassName(new IntPtr(handle), name, name.Capacity) == 0) return "";
            return name.ToString();
        }
        static bool NativeEdit(AutomationElement element) {
            string value = NativeClass(element);
            return value.Equals("Edit", StringComparison.OrdinalIgnoreCase) ||
                value.StartsWith("WindowsForms10.EDIT.", StringComparison.OrdinalIgnoreCase) ||
                value.Equals("RichEdit20W", StringComparison.OrdinalIgnoreCase) ||
                value.Equals("RICHEDIT50W", StringComparison.OrdinalIgnoreCase);
        }
        static string NativeButtonAction(AutomationElement element) {
            string name = NativeClass(element);
            bool forms = name.StartsWith("WindowsForms10.BUTTON.", StringComparison.OrdinalIgnoreCase);
            if (!forms && !name.Equals("Button", StringComparison.OrdinalIgnoreCase)) return "";
            ControlType type = element.Current.ControlType;
            if (type == ControlType.Button) return "invoke";
            // WinForms handles its checkbox/radio state in the command handler.
            // Other frameworks need their own verified state/notification adapter.
            if (forms && type == ControlType.CheckBox) return "toggle";
            if (forms && type == ControlType.RadioButton) return "select";
            return "";
        }
        static void SetNativeValue(Target target, AutomationElement element, string value) {
            if (!NativeEdit(element)) Fail("unsupported_action");
            IntPtr handle = new IntPtr(element.Current.NativeWindowHandle);
            uint pid;
            GetWindowThreadProcessId(handle, out pid);
            if (pid != target.PID || GetAncestor(handle, 2) != target.Handle) Fail("stale_target");
            int style = GetWindowLong(handle, -16);
            if (element.Current.IsPassword || (style & 0x20) != 0) Fail("password_element");
            if ((style & 0x800) != 0) Fail("element_read_only");
            IntPtr result;
            // WM_SETTEXT changes the chosen Edit value, not the desktop input stream.
            // Legacy UIA ValuePattern.SetValue may activate the target window.
            if (SendTextTimeout(handle, 0x000C, IntPtr.Zero, value, 3, 1500, out result) == IntPtr.Zero ||
                result == IntPtr.Zero) Fail("native_unavailable");
        }
        static void NativeButtonCommand(Target target, AutomationElement element, string kind) {
            if (NativeButtonAction(element) != kind) Fail("unsupported_action");
            IntPtr handle = new IntPtr(element.Current.NativeWindowHandle);
            IntPtr parent = GetParent(handle);
            uint pid;
            GetWindowThreadProcessId(handle, out pid);
            if (pid != target.PID || parent == IntPtr.Zero || GetAncestor(handle, 2) != target.Handle ||
                GetAncestor(parent, 2) != target.Handle) Fail("stale_target");
            int id = GetDlgCtrlID(handle);
            object pattern;
            if (kind == "select" && element.TryGetCurrentPattern(SelectionItemPattern.Pattern, out pattern) &&
                ((SelectionItemPattern)pattern).Current.IsSelected) return;
            IntPtr result;
            // WinForms ButtonBase handles BM_CLICK as OnClick/PerformClick,
            // including owner-drawn checkboxes which ignore WM_COMMAND.
            // Do not use this path for arbitrary native Button implementations.
            if (NativeClass(element).StartsWith("WindowsForms10.BUTTON.", StringComparison.OrdinalIgnoreCase)) {
                if (SendControlTimeout(handle, 0x00F5, IntPtr.Zero, IntPtr.Zero, 3, 1500, out result) == IntPtr.Zero)
                    Fail("native_unavailable");
                return;
            }
            // BN_CLICKED to the existing parent routes a native pushbutton command.
            if (SendControlTimeout(parent, 0x0111, new IntPtr(id & 0xffff), handle, 3, 1500, out result) == IntPtr.Zero)
                Fail("native_unavailable");
        }
        static List<string> Actions(AutomationElement element) {
            var actions = new List<string>();
            if (element.Current.IsPassword || !element.Current.IsEnabled) return actions;
            IntPtr handle = new IntPtr(element.Current.NativeWindowHandle);
            if (handle == IntPtr.Zero || !IsWindowVisible(handle)) return actions;
            object pattern;
            string buttonAction = NativeButtonAction(element);
            if (buttonAction != "") actions.Add(buttonAction);
            if (NativeEdit(element) && element.TryGetCurrentPattern(ValuePattern.Pattern, out pattern) &&
                !((ValuePattern)pattern).Current.IsReadOnly) actions.Add("set_value");
            return actions;
        }
        static List<AutomationElement> Elements(Target target) {
            AutomationElement root = AutomationElement.FromHandle(target.Handle);
            if (root == null || root.Current.ProcessId != target.PID) Fail("stale_target");
            var result = new List<AutomationElement>();
            var pending = new Stack<KeyValuePair<AutomationElement, int>>();
            pending.Push(new KeyValuePair<AutomationElement, int>(root, 0));
            int nodes = 0;
            TreeWalker walker = TreeWalker.ControlViewWalker;
            while (pending.Count > 0) {
                var item = pending.Pop();
                AutomationElement element = item.Key;
                if (++nodes > MaxNodes || item.Value > 64) Fail("element_limit");
                if (element.Current.ProcessId != target.PID) continue;
                int nativeHandle = element.Current.NativeWindowHandle;
                if (nativeHandle != 0 && GetAncestor(new IntPtr(nativeHandle), 2) != target.Handle) continue;
                if (!element.Current.IsOffscreen || element.Current.IsPassword) {
                    if (result.Count >= MaxElements) Fail("element_limit");
                    result.Add(element);
                }
                if (element.Current.IsPassword) continue;
                AutomationElement child = walker.GetFirstChild(element);
                while (child != null) {
                    if (pending.Count + nodes >= MaxNodes) Fail("element_limit");
                    pending.Push(new KeyValuePair<AutomationElement, int>(child, item.Value + 1));
                    child = walker.GetNextSibling(child);
                }
            }
            return result;
        }
        static Dictionary<string, object> Describe(AutomationElement element, Rect rect) {
            bool password = element.Current.IsPassword;
            var current = element.Current;
            var bounds = current.BoundingRectangle;
            object pattern;
            string value = "";
            if (!password && element.TryGetCurrentPattern(TogglePattern.Pattern, out pattern)) {
                value = ((TogglePattern)pattern).Current.ToggleState.ToString();
            } else if (!password && element.TryGetCurrentPattern(SelectionItemPattern.Pattern, out pattern)) {
                value = ((SelectionItemPattern)pattern).Current.IsSelected ? "Selected" : "Unselected";
            } else if (!password && element.TryGetCurrentPattern(ValuePattern.Pattern, out pattern)) {
                if (element.Current.IsPassword) Fail("password_element");
                value = Bounded(((ValuePattern)pattern).Current.Value);
            }
            double x = bounds.IsEmpty ? 0 : bounds.X - rect.Left;
            double y = bounds.IsEmpty ? 0 : bounds.Y - rect.Top;
            double width = bounds.IsEmpty ? 0 : bounds.Width;
            double height = bounds.IsEmpty ? 0 : bounds.Height;
            // Shell-cloaked controls may have empty UIA bounds. Native rectangles are
            // read-only and the element's process/root ancestry was checked by Elements.
            if (current.NativeWindowHandle != 0 && (current.IsOffscreen || width <= 0 || height <= 0)) {
                Rect nativeBounds;
                if (GetWindowRect(new IntPtr(current.NativeWindowHandle), out nativeBounds)) {
                    x = (double)nativeBounds.Left - rect.Left;
                    y = (double)nativeBounds.Top - rect.Top;
                    width = (double)nativeBounds.Right - nativeBounds.Left;
                    height = (double)nativeBounds.Bottom - nativeBounds.Top;
                }
            }
            return Obj("id", ElementID(element), "name", password ? "" : Bounded(current.Name),
                "role", password ? "password" : current.ControlType.ProgrammaticName, "value", value,
                "actions", password ? new List<string>() : Actions(element),
                "x", x, "y", y, "width", width, "height", height);
        }
        static string Capture(Target target, Rect rect, List<object> descriptions, out string error) {
            error = "";
            long width = (long)rect.Right - rect.Left, height = (long)rect.Bottom - rect.Top;
            if (IsIconic(target.Handle)) { error = "window_minimized"; return ""; }
            if (width <= 0 || height <= 0 || width > 8192 || height > 8192 || width * height > MaxPixels) {
                error = "capture_size_limit"; return "";
            }
            foreach (Dictionary<string, object> description in descriptions) {
                if ((string)description["role"] != "password") continue;
                double x = (double)description["x"], y = (double)description["y"];
                double w = (double)description["width"], h = (double)description["height"];
                if (double.IsNaN(x + y + w + h) || double.IsInfinity(x + y + w + h) ||
                    w <= 0 || h <= 0 || x < 0 || y < 0 || x + w > width || y + h > height) {
                    error = "password_bounds_unavailable"; return "";
                }
            }
            try {
                using (var bitmap = new Bitmap((int)width, (int)height, PixelFormat.Format32bppArgb)) {
                    using (Graphics graphics = Graphics.FromImage(bitmap)) {
                        IntPtr dc = graphics.GetHdc();
                        try {
                            if (!PrintWindow(target.Handle, dc, 2)) { error = "window_capture_unavailable"; return ""; }
                        } finally { graphics.ReleaseHdc(dc); }
                    }
                    // Some GPU windows report success without painting their content.
                    bool varied = false;
                    int first = bitmap.GetPixel(0, 0).ToArgb();
                    for (int y = 0; y < height && !varied; y += Math.Max(1, (int)height / 32))
                        for (int x = 0; x < width && !varied; x += Math.Max(1, (int)width / 32))
                            varied = bitmap.GetPixel(x, y).ToArgb() != first;
                    if (!varied) { error = "window_capture_blank"; return ""; }
                    using (Graphics graphics = Graphics.FromImage(bitmap)) {
                        foreach (Dictionary<string, object> description in descriptions) {
                            if ((string)description["role"] != "password") continue;
                            double x = Math.Max(0, (double)description["x"]), y = Math.Max(0, (double)description["y"]);
                            double right = Math.Min(width, (double)description["x"] + (double)description["width"]);
                            double bottom = Math.Min(height, (double)description["y"] + (double)description["height"]);
                            if (right > x && bottom > y)
                                graphics.FillRectangle(Brushes.Black, (float)x, (float)y, (float)(right - x), (float)(bottom - y));
                        }
                    }
                    using (var stream = new MemoryStream()) {
                        bitmap.Save(stream, ImageFormat.Png);
                        if (stream.Length > 6 * 1024 * 1024) { error = "capture_size_limit"; return ""; }
                        return Convert.ToBase64String(stream.ToArray());
                    }
                }
            } catch { error = "window_capture_unavailable"; return ""; }
        }
        static object Observe(Target target) {
            Focus focus = Focus.Read(target.Thread);
            try {
                Rect rect;
                if (!GetWindowRect(target.Handle, out rect)) Fail("stale_target");
                var descriptions = new List<object>();
                foreach (AutomationElement element in Elements(target)) descriptions.Add(Describe(element, rect));
                string error;
                string image = Capture(target, rect, descriptions, out error);
                RevalidateTarget(target);
                return Obj("window", target.Window, "elements", descriptions, "image", image,
                    "width", Math.Max(0L, (long)rect.Right - rect.Left), "height", Math.Max(0L, (long)rect.Bottom - rect.Top),
                    "capture_error", error);
            } finally { focus.Check(); }
        }
        static void Act(Target target, Dictionary<string, object> action) {
            string kind = Text(action, "kind"), id = Text(action, "element_id"), value = Text(action, "value");
            if (id.Length == 0 || id.Length > 1024 || value.Length > 32768) Fail("invalid_request");
            if (kind != "invoke" && kind != "set_value" && kind != "toggle" &&
                kind != "select" && kind != "expand" && kind != "collapse") Fail("unsupported_action");
            if (kind != "set_value" && value != "") Fail("invalid_request");
            Focus focus = Focus.Read(target.Thread);
            try {
            AutomationElement selected = null;
            foreach (AutomationElement element in Elements(target)) {
                if (ElementID(element) == id) { selected = element; break; }
            }
            if (selected == null) Fail("element_unavailable");
            if (selected.Current.IsPassword) Fail("password_element");
            if (!selected.Current.IsEnabled) Fail("element_disabled");
            if (!Actions(selected).Contains(kind)) Fail("unsupported_action");
            RevalidateTarget(target);
            focus.Check();
            // Only the known native adapters above may mutate controls.
            // UIA mutation proxies may focus the window, so there is no generic fallback.
            try {
                if (selected.Current.IsPassword) Fail("password_element");
                if (kind == "set_value") {
                    var pattern = (ValuePattern)selected.GetCurrentPattern(ValuePattern.Pattern);
                    if (pattern.Current.IsReadOnly) Fail("element_read_only");
                    SetNativeValue(target, selected, value);
                } else NativeButtonCommand(target, selected, kind);
            } finally {
                focus.Check();
            }
            // Detect common deferred provider focus effects; effects after this interval cannot be undone.
            Thread.Sleep(100);
            focus.Check();
            } finally { focus.Check(); }
        }
        public static string Run(string requestJSON) {
            try {
                if (SetThreadDpiAwarenessContext(new IntPtr(-4)) == IntPtr.Zero) Fail("native_unavailable");
                var request = JSON.Deserialize<Dictionary<string, object>>(requestJSON);
                string operation = Text(request, "operation");
                if (operation == "windows") {
                    var windows = new List<object>();
                    EnumWindows(delegate(IntPtr hwnd, IntPtr unused) {
                        if (windows.Count >= 512) return false;
                        try { windows.Add(InspectPickerWindow(hwnd).Window); } catch { }
                        return true;
                    }, IntPtr.Zero);
                    return JSON.Serialize(Obj("ok", true, "windows", windows));
                }
                Target target = Resolve(Text(request, "window_id"));
                if (operation == "observe") return JSON.Serialize(Obj("ok", true, "observation", Observe(target)));
                if (operation == "act") {
                    object action;
                    if (!request.TryGetValue("action", out action) || !(action is Dictionary<string, object>)) Fail("invalid_request");
                    Act(target, (Dictionary<string, object>)action);
                    return JSON.Serialize(Obj("ok", true));
                }
                Fail("invalid_request");
            } catch (NativeFailure ex) {
                return JSON.Serialize(Obj("ok", false, "error", ex.Code));
            } catch (ElementNotAvailableException) {
                return JSON.Serialize(Obj("ok", false, "error", "element_unavailable"));
            } catch (ElementNotEnabledException) {
                return JSON.Serialize(Obj("ok", false, "error", "element_disabled"));
            } catch {
                return JSON.Serialize(Obj("ok", false, "error", "native_unavailable"));
            }
            return JSON.Serialize(Obj("ok", false, "error", "invalid_request"));
        }
    }
}
