namespace AHADesktop {
    using System;
    using System.Collections.Generic;
    using System.Drawing;
    using System.Drawing.Imaging;
    using System.IO;
    using System.Runtime.InteropServices;

    public static partial class Native {
        static readonly Guid FixtureA = new Guid("00000000-0000-0000-0000-000000000001");
        static readonly Guid FixtureB = new Guid("00000000-0000-0000-0000-000000000002");
        static Guid fixtureDesktop = FixtureA;
        static IntPtr fixtureForeground, fixtureNextForeground, fixtureMovingWindow;
        static string fixtureDenial = "";
        static string fixtureChildTrustDenial = "";
        static IntPtr fixtureAccessibleHost;
        static int fixtureCaptures, fixtureSwitches, fixtureInputs;
        static bool fixtureMoveDuringSwitch;
        static readonly Dictionary<IntPtr, Guid> fixtureLocations = new Dictionary<IntPtr, Guid>();
        static readonly Dictionary<IntPtr, int> fixtureCloaks = new Dictionary<IntPtr, int>();
        static readonly List<string> fixtureCases = new List<string>();
        [DllImport("user32.dll", EntryPoint = "CreateWindowExW", CharSet = CharSet.Unicode)]
        static extern IntPtr FixtureCreateWindow(int ex, string cls, string title, uint style,
            int x, int y, int width, int height, IntPtr parent, IntPtr menu, IntPtr instance, IntPtr parameter);
        [DllImport("user32.dll", EntryPoint = "ShowWindow")] static extern bool FixtureShowWindow(IntPtr hwnd, int command);
        [DllImport("user32.dll", EntryPoint = "DestroyWindow")] static extern bool FixtureDestroyWindow(IntPtr hwnd);

        static IntPtr GetForegroundWindow() { return fixtureForeground; }
        static int DwmGetWindowAttribute(IntPtr hwnd, int attr, out int value, int size) {
            if (!fixtureCloaks.TryGetValue(hwnd, out value)) return -1;
            return 0;
        }
        static Guid CurrentVirtualDesktop() { return fixtureDesktop; }
        static PhysicalMonitor ResolveMonitor(string id, bool allowDefault) {
            if (id != "fixture-monitor") Fail("monitor_changed");
            return new PhysicalMonitor { ID = id, Handle = new IntPtr(1),
                Bounds = new Rect { Right = 800, Bottom = 600 }, Primary = true };
        }
        static Target InspectIdentity(IntPtr hwnd) {
            if (fixtureDenial != "") Fail(fixtureDenial);
            if (!fixtureLocations.ContainsKey(hwnd)) throw new Exception("foreign_handle_inspection");
            return NativeInspectIdentity(hwnd);
        }
        static Target InspectWindowProcessIdentity(IntPtr hwnd, bool describeWindow) {
            if (!describeWindow && fixtureChildTrustDenial != "") Fail(fixtureChildTrustDenial);
            return NativeInspectWindowProcessIdentity(hwnd, describeWindow);
        }
        static int BrowserAccessibleWindow(Accessibility.IAccessible accessible, out IntPtr hwnd) {
            hwnd = fixtureAccessibleHost;
            return 0;
        }
        static bool TryWindowDesktop(IntPtr hwnd, out Guid desktop, out bool current) {
            current = false;
            if (!fixtureLocations.TryGetValue(hwnd, out desktop)) return false;
            current = desktop == fixtureDesktop;
            return true;
        }
        static List<Guid> VerifiedDesktopOrder(out Guid current) {
            current = fixtureDesktop;
            return new List<Guid> { FixtureA, FixtureB };
        }
        static string DesktopName(Guid id, int index) { return "Fixture " + (index + 1); }
        static void SwitchVirtualDesktop(Guid selected) {
            if (selected != FixtureA && selected != FixtureB) Fail("desktop_identity_unavailable");
            fixtureSwitches++;
            fixtureDesktop = selected;
            if (fixtureMoveDuringSwitch) fixtureLocations[fixtureMovingWindow] = FixtureA;
            var handles = new List<IntPtr>(fixtureLocations.Keys);
            foreach (IntPtr hwnd in handles) fixtureCloaks[hwnd] = fixtureLocations[hwnd] == selected ? 0 : 2;
        }
        static void SendBatch(List<Input> events, List<Input> releases) {
            fixtureInputs++;
            throw new Exception("physical_input_forbidden");
        }
        static string FixtureImage(bool jpeg) {
            fixtureCaptures++;
            if (fixtureNextForeground != IntPtr.Zero) {
                fixtureForeground = fixtureNextForeground;
                fixtureNextForeground = IntPtr.Zero;
            }
            using (var image = new Bitmap(2, 2))
            using (var stream = new MemoryStream()) {
                image.SetPixel(0, 0, Color.Red);
                image.SetPixel(1, 1, Color.Blue);
                image.Save(stream, jpeg ? ImageFormat.Jpeg : ImageFormat.Png);
                return Convert.ToBase64String(stream.ToArray());
            }
        }
        static string CaptureDesktop(Rect bounds, out string error) {
            error = ""; return FixtureImage(false);
        }
        static string CapturePreview(Surface surface, int width, int height, int quality,
            out int previewWidth, out int previewHeight, out string error) {
            previewWidth = previewHeight = 2; error = ""; return FixtureImage(true);
        }
        static IntPtr FixtureWindow(Guid desktop) {
            IntPtr hwnd = FixtureCreateWindow(0, "STATIC", "AHA owned access fixture", 0x00CF0000,
                24, 24, 320, 220, IntPtr.Zero, IntPtr.Zero, IntPtr.Zero, IntPtr.Zero);
            if (hwnd == IntPtr.Zero) throw new Exception("fixture_create_failed");
            fixtureLocations.Add(hwnd, desktop);
            fixtureCloaks.Add(hwnd, desktop == fixtureDesktop ? 0 : 2);
            FixtureShowWindow(hwnd, 4);
            return hwnd;
        }
        static void FixtureCheck(bool result, string label) {
            if (!result) throw new Exception("fixture_" + label);
            fixtureCases.Add(label);
        }
        static void FixtureError(Action operation, string code, string label) {
            try { operation(); } catch (NativeFailure e) {
                FixtureCheck(e.Code == code, label); return;
            }
            throw new Exception("fixture_expected_error_" + label);
        }
        static Dictionary<string, object> FixtureRead(string id, bool preview) {
            if (!preview) return (Dictionary<string, object>)ObserveForegroundNative(id);
            var result = JSON.Deserialize<Dictionary<string, object>>(RunPreview(JSON.Serialize(
                Obj("window_id", id, "frame", Obj("max_width", 800, "max_height", 600, "quality", 65)))));
            if (!(bool)result["ok"]) Fail(Text(result, "error"));
            return (Dictionary<string, object>)result["observation"];
        }
        static int FixtureActionCount(Dictionary<string, object> observation) {
            return ((System.Collections.ICollection)observation["input_actions"]).Count;
        }
        public static string TestDesktopAccess() {
            IntPtr actualForeground = FixtureRealForegroundWindow();
            IntPtr a = IntPtr.Zero, b = IntPtr.Zero;
            try {
                a = FixtureWindow(FixtureA);
                b = FixtureWindow(FixtureA);
                fixtureForeground = a;
                string scope = "desktop:" + FixtureA.ToString("D") + "@fixture-monitor";
                foreach (bool preview in new bool[] { false, true }) {
                    fixtureCloaks[a] = 0;
                    var normal = FixtureRead(scope, preview);
                    FixtureCheck(Text(normal, "image") != "" && FixtureActionCount(normal) == 7, "normal_" + preview);
                    fixtureCloaks[a] = 2;
                    var cloaked = FixtureRead(scope, preview);
                    FixtureCheck(Text(cloaked, "image") != "" && FixtureActionCount(cloaked) == 0 &&
                        Text(cloaked, "control_error") == "desktop_foreground_unavailable", "cloaked_readonly_" + preview);
                    FixtureError(delegate { ReadSurface(scope); }, "unsupported_target", "cloaked_input_rejected_" + preview);
                    fixtureCloaks[a] = 0;
                    FixtureShowWindow(a, 0);
                    var hidden = FixtureRead(scope, preview);
                    FixtureCheck(Text(hidden, "image") != "" && FixtureActionCount(hidden) == 0, "hidden_readonly_" + preview);
                    FixtureShowWindow(a, 4);
                    fixtureNextForeground = b;
                    var changed = FixtureRead(scope, preview);
                    FixtureCheck(Text(changed, "image") != "" && FixtureActionCount(changed) == 0 &&
                        Text(changed, "control_error") == "desktop_foreground_changed", "transition_readonly_" + preview);
                    FixtureError(delegate {
                        ForegroundAction(Obj("window_id", scope, "surface", changed["surface"], "width", 800, "height", 600,
                            "action", Obj("element_id", "$surface", "kind", "key", "keys", new string[] { "ENTER" })));
                    }, "refresh_required", "transition_input_rejected_" + preview);
                    fixtureForeground = a;
                    foreach (string denial in new string[] { "target_foreign_user", "target_foreign_session",
                        "elevated_target", "target_process_unavailable", "target_token_unavailable" }) {
                        fixtureDenial = denial;
                        int before = fixtureCaptures;
                        FixtureError(delegate { FixtureRead(scope, preview); }, denial, "trust_" + denial + "_" + preview);
                        FixtureCheck(before == fixtureCaptures, "no_capture_" + denial + "_" + preview);
                    }
                    fixtureDenial = "stale_target";
                    FixtureError(delegate { FixtureRead(scope, preview); }, "foreground_unavailable", "gone_foreground_" + preview);
                    fixtureDenial = "";
                    fixtureForeground = IntPtr.Zero;
                    FixtureError(delegate { FixtureRead(scope, preview); }, "foreground_unavailable", "missing_foreground_" + preview);
                    fixtureForeground = a;
                }
                var known = new List<Guid> { FixtureA, FixtureB };
                fixtureLocations[b] = FixtureB;
                fixtureCloaks[b] = 2;
                Target other = InspectTargetWindow(b, FixtureA, known);
                FixtureCheck((bool)other.Window["other_desktop"] && Text(other.Window, "desktop_id") == FixtureB.ToString("D"),
                    "other_desktop_metadata");
                FixtureCheck(fixtureSwitches == 0, "enumeration_has_no_switch");
                FixtureError(delegate { InspectPickerWindow(b); }, "unsupported_target", "legacy_current_desktop_only");
                foreach (int cloak in new int[] { 1, 3, 4, 6 }) {
                    fixtureCloaks[b] = cloak;
                    FixtureError(delegate { InspectTargetWindow(b, FixtureA, known); }, "unsupported_target", "app_cloak_" + cloak);
                }
                fixtureCloaks[b] = 2;
                FixtureError(delegate { InspectTargetWindow(b, FixtureA, new List<Guid> { FixtureA }); },
                    "desktop_identity_unavailable", "unknown_desktop_rejected");
                string wrongID = "0.0." + b.ToInt64().ToString("x");
                FixtureError(delegate { SelectNativeTarget(Obj("kind", "window", "window_id", wrongID)); },
                    "stale_target", "stale_identity_before_switch");
                FixtureCheck(fixtureSwitches == 0, "stale_identity_no_switch");
                Target background = ResolveBackground(other.ID, Guid.Empty, FixtureA, true);
                FixtureCheck(background.IncludeOffscreen && Text(background.Window, "desktop_id") == FixtureB.ToString("D"),
                    "background_selection_pins_other_desktop");
                background.Revalidate();
                FixtureCheck(fixtureDesktop == FixtureA && fixtureSwitches == 0, "background_selection_no_switch");
                FixtureError(delegate { ResolveBackground(other.ID, FixtureA, FixtureA, false); },
                    "background_desktop_changed", "background_wrong_binding_rejected");
                FixtureError(delegate { ResolveBackground(wrongID, FixtureB, FixtureA, false); },
                    "stale_target", "background_stale_identity_rejected");
                foreach (int cloak in new int[] { 1, 3, 4, 6 }) {
                    fixtureCloaks[b] = cloak;
                    FixtureError(delegate { ResolveBackground(other.ID, FixtureB, FixtureA, false); },
                        "unsupported_target", "background_app_cloak_" + cloak);
                }
                fixtureCloaks[b] = 2;
                FixtureShowWindow(b, 0);
                FixtureError(delegate { ResolveBackground(other.ID, FixtureB, FixtureA, false); },
                    "stale_target", "background_hidden_rejected");
                FixtureShowWindow(b, 4);
                foreach (string denial in new string[] { "target_foreign_user", "target_foreign_session", "elevated_target" }) {
                    fixtureDenial = denial;
                    FixtureError(delegate { ResolveBackground(other.ID, FixtureB, FixtureA, false); },
                        denial, "background_trust_" + denial);
                }
                fixtureDenial = "";
                fixtureDesktop = FixtureB;
                FixtureError(delegate { background.Revalidate(); }, "background_context_changed", "background_context_change");
                fixtureDesktop = FixtureA;
                fixtureLocations[b] = FixtureA;
                FixtureError(delegate { background.Revalidate(); }, "background_desktop_changed", "background_target_moved");
                fixtureLocations[b] = FixtureB;
                FixtureCheck(fixtureSwitches == 0 && fixtureInputs == 0, "background_guards_never_switch_or_input");
                Rect fixtureBounds;
                if (!GetWindowRect(b, out fixtureBounds)) throw new Exception("fixture_bounds");
                foreach (double width in new double[] { 0, -1, double.NaN, double.PositiveInfinity, 10000 }) {
                    string captureError;
                    var invalidMask = new List<object> { Obj("role", "password", "x", 0.0, "y", 0.0,
                        "width", width, "height", 10.0) };
                    string image = Capture(background, fixtureBounds, invalidMask, out captureError);
                    FixtureCheck(image == "" && captureError == "password_bounds_unavailable",
                        "background_invalid_mask_" + width);
                }
                IntPtr child = FixtureCreateWindow(0, "EDIT", "receipt-fixture", 0x50000000,
                    4, 4, 100, 24, b, new IntPtr(321), IntPtr.Zero, IntPtr.Zero);
                if (child == IntPtr.Zero) throw new Exception("fixture_child_create");
                try {
                    PrepareBackgroundLifetime(background);
                    backgroundLifetime.Watch(child);
                    var control = InspectBackgroundControl(background, child);
                    string receipt = RememberBackgroundControl(background, control, backgroundLifetime.Read());
                    // Simulate destroy/create notifications for the SAME numeric
                    // HWND; no metadata changes are needed to invalidate the ID.
                    var callback = typeof(BackgroundLifetime).GetMethod("OnEvent",
                        System.Reflection.BindingFlags.Instance | System.Reflection.BindingFlags.NonPublic);
                    callback.Invoke(backgroundLifetime, new object[] { IntPtr.Zero, (uint)0x8001, child, 0, 0, (uint)0, (uint)0 });
                    callback.Invoke(backgroundLifetime, new object[] { IntPtr.Zero, (uint)0x8000, child, 0, 0, (uint)0, (uint)0 });
                    FixtureError(delegate { ActBackgroundNative(background,
                        Obj("element_id", receipt, "kind", "set_value", "value", "must-not-write")); },
                        "refresh_required", "background_same_hwnd_reuse_rejected");
                    FixtureError(delegate { ActBackgroundNative(background,
                        Obj("element_id", control.ID, "kind", "set_value", "value", "must-not-write")); },
                        "refresh_required", "background_raw_hwnd_rejected");
                } finally { FixtureDestroyWindow(child); }
                var selected = (Dictionary<string, object>)SelectNativeTarget(Obj("kind", "window", "window_id", other.ID));
                FixtureCheck(fixtureDesktop == FixtureB && fixtureSwitches == 1 && Text(selected, "id") == other.ID &&
                    !(bool)selected["other_desktop"], "explicit_selection_switches_once");
                fixtureDesktop = FixtureA; fixtureCloaks[b] = 2;
                fixtureMovingWindow = b; fixtureMoveDuringSwitch = true;
                FixtureError(delegate { SelectNativeTarget(Obj("kind", "window", "window_id", other.ID)); },
                    "stale_target", "moved_window_cannot_be_granted");
                FixtureCheck(fixtureInputs == 0, "no_native_input");
                var frame = new Rect { Right = 800, Bottom = 600 };
                var browserState = new BrowserState { Role = 42, State = 0, HostEnabled = true,
                    Bounds = new Rect { Left = 20, Top = 20, Right = 120, Bottom = 44 }, DefaultAction = "" };
                FixtureCheck(BrowserAction(browserState, frame) == "set_value", "browser_edit_advertised");
                foreach (int denied in new int[] { BrowserProtected, BrowserInvisible, BrowserDisabled, BrowserReadOnly }) {
                    browserState.State = denied;
                    FixtureCheck(BrowserAction(browserState, frame) == "", "browser_state_rejected_" + denied);
                }
                browserState.State = BrowserProtected;
                FixtureCheck(BrowserRole(browserState) == "password", "browser_password_role");
                browserState.State = 0;
                browserState.HostEnabled = false;
                FixtureCheck(BrowserAction(browserState, frame) == "", "browser_disabled_ancestor");
                browserState.HostEnabled = true;
                browserState.Role = 30; browserState.DefaultAction = "click";
                FixtureCheck(BrowserAction(browserState, frame) == "invoke", "browser_link_advertised");
                browserState.Role = 43;
                FixtureCheck(BrowserAction(browserState, frame) == "invoke", "browser_button_advertised");
                browserState.DefaultAction = "";
                FixtureCheck(BrowserAction(browserState, frame) == "", "browser_missing_default_action");
                browserState.Role = 3; browserState.DefaultAction = "click";
                FixtureCheck(BrowserAction(browserState, frame) == "", "browser_unknown_role_rejected");
                browserState.Role = 30; browserState.Bounds.Right = 900;
                FixtureCheck(BrowserAction(browserState, frame) == "", "browser_outside_frame_rejected");
                Target browserTarget = InspectIdentity(a);
                fixtureAccessibleHost = a;
                FixtureCheck(BrowserHost(browserTarget, null, new Dictionary<IntPtr, Target>()).Handle == a,
                    "browser_same_window_host_verified");
                fixtureAccessibleHost = b;
                FixtureError(delegate { BrowserHost(browserTarget, null, new Dictionary<IntPtr, Target>()); },
                    "browser_scope_changed", "browser_other_window_host_rejected");
                fixtureAccessibleHost = a;
                foreach (string denied in new string[] { "target_foreign_user", "target_foreign_session", "elevated_target" }) {
                    fixtureChildTrustDenial = denied;
                    FixtureError(delegate { BrowserHost(browserTarget, null, new Dictionary<IntPtr, Target>()); },
                        denied, "browser_host_trust_" + denied);
                }
                fixtureChildTrustDenial = "";
            } finally {
                if (b != IntPtr.Zero) FixtureDestroyWindow(b);
                if (a != IntPtr.Zero) FixtureDestroyWindow(a);
            }
            return JSON.Serialize(Obj("ok", true, "cases", fixtureCases,
                "foreground_unchanged", actualForeground == FixtureRealForegroundWindow(), "native_input_calls", fixtureInputs));
        }
    }
}
