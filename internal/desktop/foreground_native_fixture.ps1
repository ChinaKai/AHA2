$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$packet = [Console]::In.ReadLine() | ConvertFrom-Json
Add-Type -TypeDefinition $packet.source -ReferencedAssemblies Accessibility,UIAutomationClient,UIAutomationTypes,WindowsBase,System.Drawing,System.Web.Extensions
$fixture = @'
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Threading;
using System.Web.Script.Serialization;
using System.Windows.Forms;

public class AHAForegroundFixture : Form {
    [DllImport("user32.dll")] static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
    [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr hwnd, out R rect);
    [DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] static extern bool IsWindowEnabled(IntPtr hwnd);
    [DllImport("user32.dll")] static extern bool EnableWindow(IntPtr hwnd, bool enabled);
    [DllImport("user32.dll", EntryPoint = "CreateWindowExW", CharSet = CharSet.Unicode)]
    static extern IntPtr CreateChild(int extended, string cls, string title, uint style, int x, int y,
        int width, int height, IntPtr parent, IntPtr menu, IntPtr instance, IntPtr parameter);
    [DllImport("user32.dll")] static extern bool DestroyWindow(IntPtr hwnd);
    [DllImport("user32.dll")] static extern IntPtr WindowFromPoint(Point point);
    [DllImport("user32.dll")] static extern IntPtr GetAncestor(IntPtr hwnd, uint flags);
    [DllImport("user32.dll")] static extern int GetSystemMetrics(int index);
    [DllImport("user32.dll")] static extern uint SendInput(uint count, Input[] input, int size);
    [StructLayout(LayoutKind.Sequential)] struct R { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)] struct Input { public uint Type; public U Data; }
    [StructLayout(LayoutKind.Explicit)] struct U {
        [FieldOffset(0)] public K Key;
        [FieldOffset(0)] public M Mouse;
    }
    [StructLayout(LayoutKind.Sequential)] struct K {
        public ushort Key, Scan; public uint Flags, Time; public UIntPtr Extra;
    }
    [StructLayout(LayoutKind.Sequential)] struct M {
        public int X, Y; public uint Data, Flags, Time; public UIntPtr Extra;
    }
    static readonly JavaScriptSerializer JSON = new JavaScriptSerializer();
    [ComImport, Guid("A5CD92FF-29BE-454C-8D04-D82879FB3F1B"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface DesktopManager {
        [PreserveSig] int Current(IntPtr hwnd, [MarshalAs(UnmanagedType.Bool)] out bool current);
        [PreserveSig] int Identity(IntPtr hwnd, out Guid id);
        [PreserveSig] int Move(IntPtr hwnd, ref Guid id);
    }
    public class GuardEdit : TextBox {
        public bool PasswordOnRead;
        public void Rebuild() { RecreateHandle(); }
        protected override void WndProc(ref Message m) {
            bool change = PasswordOnRead && m.Msg == 0x000D;
            if (change) PasswordOnRead = false;
            base.WndProc(ref m);
            if (change) UseSystemPasswordChar = true;
        }
    }
    readonly GuardEdit edit = new GuardEdit { Text = "fixture-initial", Location = new Point(20, 20), Width = 330 };
    readonly Button button = new Button { Text = "Fixture Click", Location = new Point(20, 60), Width = 180 };
    readonly Canvas canvas = new Canvas { Location = new Point(20, 110), Size = new Size(350, 130), BackColor = Color.LightGreen };
    Form other, modal;
    TextBox passwordControl;
    bool movePasswordAfterPrint;
    int clicks, enterKeys, tabKeys;
    Guid initialDesktop;
    Guid createdDesktop;
    readonly HashSet<Guid> ownedDesktops = new HashSet<Guid>();
    readonly List<IntPtr> ownedChildren = new List<IntPtr>();
    protected override void WndProc(ref Message m) {
        bool change = movePasswordAfterPrint && (m.Msg == 0x0317 || m.Msg == 0x0318);
        if (change) movePasswordAfterPrint = false;
        base.WndProc(ref m);
        if (change && passwordControl != null) passwordControl.Left += 8;
    }
    public AHAForegroundFixture() {
        Text = "AHA foreground owned fixture";
        StartPosition = FormStartPosition.Manual; Location = new Point(40, 40);
        Size = new Size(440, 340); ShowInTaskbar = false;
        Controls.Add(edit); Controls.Add(button); Controls.Add(canvas);
        button.Click += delegate { clicks++; };
        edit.KeyDown += delegate(object sender, KeyEventArgs key) {
            if (key.KeyCode == Keys.Enter) enterKeys++;
            if (key.KeyCode == Keys.Tab) tabKeys++;
        };
        Shown += delegate {
            try {
            other = new Form { Text = "AHA focus blocker owned fixture", Size = new Size(230, 160),
                StartPosition = FormStartPosition.Manual, Location = new Point(540, 60), ShowInTaskbar = false };
            other.Show(); Activate(); edit.Focus();
            try { initialDesktop = DesktopIdentity(); } catch { initialDesktop = Guid.Empty; }
            Write(new { ready = true, window = Window(this), other = Window(other), initial_desktop = initialDesktop.ToString("D") });
            var reader = new Thread(ReadCommands) { IsBackground = true };
            reader.Start();
            } catch (Exception error) {
                Write(new { ready = false, error = error.GetBaseException().GetType().Name,
                    detail = error.GetBaseException().Message,
                    code = error.GetBaseException().GetType().GetField("Code") == null ? "" :
                        (string)error.GetBaseException().GetType().GetField("Code").GetValue(error.GetBaseException()) });
                Close();
            }
        };
        var timer = new System.Windows.Forms.Timer { Interval = 240000 };
        timer.Tick += delegate { Close(); };
        timer.Start();
    }
    public class Canvas : Panel {
        public int Doubles, Wheels, Drags;
        bool dragging;
        Point start;
        public Canvas() {
            SetStyle(ControlStyles.StandardDoubleClick | ControlStyles.Selectable, true); TabStop = true;
        }
        protected override void OnMouseDown(MouseEventArgs e) {
            base.OnMouseDown(e); Focus(); dragging = true; start = e.Location;
        }
        protected override void OnMouseUp(MouseEventArgs e) {
            base.OnMouseUp(e);
            if (dragging && Math.Abs(e.X - start.X) + Math.Abs(e.Y - start.Y) > 20) Drags++;
            dragging = false;
        }
        protected override void OnMouseDoubleClick(MouseEventArgs e) { base.OnMouseDoubleClick(e); Doubles++; }
        protected override void WndProc(ref Message m) {
            if (m.Msg == 0x020A || m.Msg == 0x020E) Wheels++;
            base.WndProc(ref m);
        }
    }
    static object Window(Form window) {
        using (var self = Process.GetCurrentProcess()) {
            string id = self.Id.ToString("x") + "." + self.StartTime.ToUniversalTime().ToFileTimeUtc().ToString("x") +
                "." + window.Handle.ToInt64().ToString("x");
            return new { id = id, title = window.Text, process = "powershell", kind = "window" };
        }
    }
    static Guid DesktopIdentity() {
        Type native = Type.GetType("AHADesktop.Native");
        if (native == null) {
            foreach (Assembly assembly in AppDomain.CurrentDomain.GetAssemblies()) {
                native = assembly.GetType("AHADesktop.Native");
                if (native != null) break;
            }
        }
        return (Guid)native.GetMethod("CurrentVirtualDesktop", BindingFlags.Static | BindingFlags.NonPublic).Invoke(null, null);
    }
    void OwnActivation() {
        TopMost = true;
        Point p = edit.PointToScreen(new Point(10, 10));
        if (GetAncestor(WindowFromPoint(p), 2) != Handle) throw new InvalidOperationException();
        int left = GetSystemMetrics(76), top = GetSystemMetrics(77), width = GetSystemMetrics(78), height = GetSystemMetrics(79);
        var inputs = new Input[] {
            new Input { Type = 0, Data = new U { Mouse = new M { X = (p.X-left)*65535/(width-1), Y = (p.Y-top)*65535/(height-1), Flags = 0xC001 } } },
            new Input { Type = 0, Data = new U { Mouse = new M { Flags = 2 } } },
            new Input { Type = 0, Data = new U { Mouse = new M { Flags = 4 } } }
        };
        if (SendInput(3, inputs, Marshal.SizeOf(typeof(Input))) != 3) throw new InvalidOperationException();
        var restore = new System.Windows.Forms.Timer { Interval = 350 };
        restore.Tick += delegate { TopMost = false; restore.Stop(); restore.Dispose(); };
        restore.Start();
    }
    static void Write(object value) { Console.WriteLine(JSON.Serialize(value)); Console.Out.Flush(); }
    object Position(Control control) {
        R frame; GetWindowRect(Handle, out frame);
        Point point = control.PointToScreen(new Point(control.Width / 2, control.Height / 2));
        return new { x = point.X - frame.Left, y = point.Y - frame.Top };
    }
    object DesktopPosition(Control control) {
        Point point = control.PointToScreen(new Point(control.Width / 2, control.Height / 2));
        Rectangle monitor = Screen.FromHandle(Handle).Bounds;
        return new { x = point.X - monitor.Left, y = point.Y - monitor.Top };
    }
    void ReadCommands() {
        string line;
        while ((line = Console.ReadLine()) != null) {
            string command = line;
            try { BeginInvoke((Action)delegate { Command(command); }); } catch { return; }
        }
        try { BeginInvoke((Action)delegate { Close(); }); } catch { }
    }
    void Command(string commandJSON) {
        try {
            var command = JSON.Deserialize<Dictionary<string, object>>(commandJSON);
            string operation = (string)command["operation"];
            if (operation == "status") {
                Write(new { ok = true, text = edit.Text, clicks = clicks, doubles = canvas.Doubles,
                    wheels = canvas.Wheels, drags = canvas.Drags, minimized = WindowState == FormWindowState.Minimized,
                    foreground = GetForegroundWindow() == Handle, modal = modal != null && !modal.IsDisposed && modal.Visible,
                    enabled = IsWindowEnabled(Handle), edit = Position(edit), button = Position(button), canvas = Position(canvas),
                    enter_keys = enterKeys, tab_keys = tabKeys,
                    desktop_edit = DesktopPosition(edit), desktop_button = DesktopPosition(button), desktop_canvas = DesktopPosition(canvas) });
            } else if (operation == "minimize") { WindowState = FormWindowState.Minimized; other.Activate(); Write(new { ok = true }); }
            else if (operation == "move_monitor") {
                Location = new Point(Convert.ToInt32(command["x"]) + 40, Convert.ToInt32(command["y"]) + 40);
                Write(new { ok = true });
            }
            else if (operation == "multiline") {
                edit.Multiline = true; edit.AcceptsReturn = true; edit.AcceptsTab = true; edit.WordWrap = false;
                edit.Height = 60; button.Top = 90; canvas.Top = 140; edit.Focus();
                Write(new { ok = true });
            }
            else if (operation == "own_activation") { OwnActivation(); Write(new { ok = true }); }
            else if (operation == "background_controls") {
                button.FlatStyle = FlatStyle.System;
                var password = new TextBox { Text = "fixture-password-never-export", UseSystemPasswordChar = true,
                    Location = new Point(20, 250), Width = 200 };
                passwordControl = password;
                Controls.Add(password);
                var disabled = new TextBox { Text = "fixture-disabled", Enabled = false,
                    Location = new Point(240, 250), Width = 130 };
                Controls.Add(disabled);
                var hidden = new TextBox { Text = "fixture-hidden", Visible = false,
                    Location = new Point(20, 280), Width = 150 };
                Controls.Add(hidden);
                Write(new { ok = true });
            }
            else if (operation == "background_disabled_parent") {
                var parent = new Panel { Location = new Point(240, 215), Size = new Size(140, 30) };
                var nested = new TextBox { Text = "fixture-parent-disabled", Width = 130 };
                parent.Controls.Add(nested); Controls.Add(parent);
                IntPtr childHandle = nested.Handle;
                EnableWindow(parent.Handle, false);
                Write(new { ok = true });
            }
            else if (operation == "password_on_read") { edit.PasswordOnRead = true; Write(new { ok = true }); }
            else if (operation == "password_after_print") { movePasswordAfterPrint = true; Write(new { ok = true }); }
            else if (operation == "clear_password") { edit.UseSystemPasswordChar = false; Write(new { ok = true }); }
            else if (operation == "rebuild_edit") { edit.Rebuild(); Write(new { ok = true }); }
            else if (operation == "background_aba_enabled") {
                IntPtr before = edit.Handle;
                EnableWindow(before, false); EnableWindow(before, true);
                Write(new { ok = edit.Handle == before });
            }
            else if (operation == "cross_process_children") {
                IntPtr parent = new IntPtr(long.Parse((string)command["parent"], System.Globalization.NumberStyles.HexNumber));
                IntPtr text = CreateChild(0, "EDIT", "cross-process-text", 0x50000000, 20, 250, 160, 24,
                    parent, new IntPtr(731), IntPtr.Zero, IntPtr.Zero);
                IntPtr password = CreateChild(0, "EDIT", "cross-process-password", 0x50000020, 200, 250, 180, 24,
                    parent, new IntPtr(732), IntPtr.Zero, IntPtr.Zero);
                if (text != IntPtr.Zero) ownedChildren.Add(text);
                if (password != IntPtr.Zero) ownedChildren.Add(password);
                if (text == IntPtr.Zero || password == IntPtr.Zero) throw new InvalidOperationException();
                Write(new { ok = true });
            }
            else if (operation == "close_cross_process_children") {
                foreach (IntPtr hwnd in ownedChildren) DestroyWindow(hwnd);
                ownedChildren.Clear();
                Write(new { ok = true });
            }
            else if (operation == "desktop_diagnostic") {
                var obj = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("AA509086-5CA9-4C25-8F95-589D3C07B48A")));
                var manager = (DesktopManager)obj;
                Guid id; bool current;
                int identityHR = manager.Identity(Handle, out id), currentHR = manager.Current(Handle, out current);
                Write(new { ok = true, text = identityHR + "," + id + "," + currentHR + "," + current });
                Marshal.ReleaseComObject(obj);
            }
            else if (operation == "current_matches_initial") {
                Write(new { ok = DesktopIdentity() == initialDesktop });
            }
            else if (operation == "other") { other.Activate(); Write(new { ok = true }); }
            else if (operation == "modal") {
                modal = new Form { Text = "AHA owned modal fixture", Size = new Size(280, 180),
                    StartPosition = FormStartPosition.Manual, Location = new Point(130, 110), ShowInTaskbar = false };
                var cancel = new Button { Text = "Cancel fixture", Location = new Point(30, 50), Width = 160 };
                cancel.Click += delegate { modal.Close(); };
                modal.Controls.Add(cancel);
                modal.CancelButton = cancel;
                modal.Shown += delegate { Write(new { ok = true }); };
                modal.ShowDialog(this);
                modal.Dispose();
                Activate();
            }
            else if (operation == "activate") { Activate(); Write(new { ok = true }); }
            else if (operation == "record_desktop") {
                Guid id = Guid.Parse(((string)command["id"]).Split('@')[0]);
                if (id == initialDesktop || DesktopIdentity() != id) throw new InvalidOperationException();
                ownedDesktops.Add(id);
                createdDesktop = id; Write(new { ok = true });
            } else if (operation == "move_to_created") {
                if (createdDesktop == Guid.Empty || DesktopIdentity() != createdDesktop) throw new InvalidOperationException();
                var obj = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("AA509086-5CA9-4C25-8F95-589D3C07B48A")));
                try {
                    var manager = (DesktopManager)obj;
                    if (manager.Move(Handle, ref createdDesktop) != 0) throw new InvalidOperationException();
                } finally { Marshal.ReleaseComObject(obj); }
                Write(new { ok = true });
            } else if (operation == "move_to_initial") {
                if (initialDesktop == Guid.Empty) throw new InvalidOperationException();
                var obj = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("AA509086-5CA9-4C25-8F95-589D3C07B48A")));
                try {
                    var manager = (DesktopManager)obj;
                    if (manager.Move(Handle, ref initialDesktop) != 0) throw new InvalidOperationException();
                } finally { Marshal.ReleaseComObject(obj); }
                Write(new { ok = true });
            } else if (operation == "cleanup_desktop") {
                if (command.ContainsKey("id")) createdDesktop = Guid.Parse(((string)command["id"]).Split('@')[0]);
                if (createdDesktop == Guid.Empty || !ownedDesktops.Contains(createdDesktop) ||
                    DesktopIdentity() != createdDesktop || createdDesktop == initialDesktop)
                    throw new InvalidOperationException();
                // Only a GUID verified as created by this test may be closed.
                ushort[] keys = { 0x5B, 0x11, 0x73 };
                var events = new List<Input>();
                foreach (ushort key in keys) events.Add(new Input { Type = 1, Data = new U { Key = new K { Key = key } } });
                for (int i = keys.Length - 1; i >= 0; i--)
                    events.Add(new Input { Type = 1, Data = new U { Key = new K { Key = keys[i], Flags = 2 } } });
                if (SendInput((uint)events.Count, events.ToArray(), Marshal.SizeOf(typeof(Input))) != events.Count)
                    throw new InvalidOperationException();
                bool changed = false;
                for (int i = 0; i < 30; i++) { Thread.Sleep(100); if (DesktopIdentity() != createdDesktop) { changed = true; break; } }
                if (!changed) throw new InvalidOperationException();
                ownedDesktops.Remove(createdDesktop);
                createdDesktop = Guid.Empty; Write(new { ok = true, cleaned = true });
            } else if (operation == "close") { Write(new { ok = true }); if (modal != null) modal.Close(); other.Close(); Close(); }
            else throw new InvalidOperationException();
        } catch { Write(new { ok = false, error = "fixture_command_failed" }); }
    }
    public static void Run() {
        SetThreadDpiAwarenessContext(new IntPtr(-4));
        Application.EnableVisualStyles();
        Application.Run(new AHAForegroundFixture());
    }
}
'@
Add-Type -TypeDefinition $fixture -ReferencedAssemblies System.Windows.Forms,System.Drawing,System.Web.Extensions
[AHAForegroundFixture]::Run()
