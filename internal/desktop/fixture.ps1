$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$source = @'
using System;
using System.Diagnostics;
using System.Drawing;
using System.Runtime.InteropServices;
using System.Windows.Forms;

public class AHASharedFixture : Form {
    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    static extern IntPtr CreateWindowEx(int exStyle, string cls, string title, int style,
        int x, int y, int width, int height, IntPtr parent, IntPtr id, IntPtr instance, IntPtr param);
    IntPtr nativeButton;
    Label nativeResult;
    protected override void WndProc(ref Message message) {
        if (message.Msg == 0x0111 && message.LParam == nativeButton && nativeButton != IntPtr.Zero) {
            nativeResult.Text = "native-invoked";
            return;
        }
        base.WndProc(ref message);
    }
    protected override bool ShowWithoutActivation { get { return true; } }
    protected override CreateParams CreateParams {
        get { var p = base.CreateParams; p.ExStyle |= 0x08000000; return p; }
    }
    public AHASharedFixture() {
        Text = "AHA shared control test fixture";
        Size = new Size(460, 380);
        StartPosition = FormStartPosition.Manual;
        Location = new Point(30, 30);
        ShowInTaskbar = false;
        var edit = new TextBox { Text = "initial-fixture", Location = new Point(20, 20), Width = 330 };
        var password = new TextBox { Text = "fixture-private-sample", UseSystemPasswordChar = true,
            Location = new Point(20, 60), Width = 330 };
        var button = new Button { Text = "Fixture Invoke", Location = new Point(20, 110), Width = 150 };
        var toggle = new CheckBox { Text = "Fixture Toggle", Location = new Point(20, 160), Width = 180 };
        var radio = new RadioButton { Text = "Fixture Radio", Location = new Point(20, 200), Width = 180 };
        nativeResult = new Label { Text = "native-pending", Location = new Point(200, 290), Width = 200 };
        button.Click += delegate { edit.Text = "invoked-fixture"; };
        Controls.Add(edit);
        Controls.Add(password);
        Controls.Add(button);
        Controls.Add(toggle);
        Controls.Add(radio);
        Controls.Add(nativeResult);
        Shown += delegate {
            CreateWindowEx(0, "Edit", "native-initial", 0x50000080, 20, 240, 330, 24, Handle,
                new IntPtr(401), IntPtr.Zero, IntPtr.Zero);
            nativeButton = CreateWindowEx(0, "Button", "Native Invoke", 0x50000000, 20, 280, 150, 28, Handle,
                new IntPtr(402), IntPtr.Zero, IntPtr.Zero);
            using (var self = Process.GetCurrentProcess()) {
                var id = self.Id.ToString("x") + "." + self.StartTime.ToUniversalTime().ToFileTimeUtc().ToString("x")
                    + "." + Handle.ToInt64().ToString("x");
                Console.WriteLine("{\"id\":\"" + id + "\",\"title\":\"AHA shared control test fixture\",\"process\":\"powershell\"}");
                Console.Out.Flush();
            }
        };
        var timer = new Timer { Interval = 85000 };
        timer.Tick += delegate { Close(); };
        timer.Start();
    }
    public static void Run() {
        Application.EnableVisualStyles();
        Application.Run(new AHASharedFixture());
    }
}
'@
Add-Type -TypeDefinition $source -ReferencedAssemblies 'System.Windows.Forms', 'System.Drawing'
[AHASharedFixture]::Run()
