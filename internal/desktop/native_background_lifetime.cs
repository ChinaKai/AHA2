namespace AHADesktop {
    public static partial class Native {
        delegate void BackgroundEventProc(IntPtr hook, uint eventType, IntPtr hwnd, int objectID, int childID, uint thread, uint time);
        [StructLayout(LayoutKind.Sequential)] struct BackgroundMessage {
            public IntPtr Window;
            public uint Message;
            public UIntPtr WParam;
            public IntPtr LParam;
            public uint Time;
            public int X, Y;
            public uint Private;
        }
        [DllImport("user32.dll")] static extern IntPtr SetWinEventHook(uint min, uint max, IntPtr module,
            BackgroundEventProc callback, uint process, uint thread, uint flags);
        [DllImport("user32.dll")] static extern bool UnhookWinEvent(IntPtr hook);
        [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
        [DllImport("user32.dll", EntryPoint = "GetMessageW")]
        static extern int BackgroundGetMessage(out BackgroundMessage message, IntPtr window, uint min, uint max);
        [DllImport("user32.dll", EntryPoint = "PeekMessageW")]
        static extern bool BackgroundPeekMessage(out BackgroundMessage message, IntPtr window, uint min, uint max, uint remove);
        [DllImport("user32.dll", EntryPoint = "PostThreadMessageW")]
        static extern bool BackgroundPostThreadMessage(uint thread, uint message, UIntPtr wParam, IntPtr lParam);

        // Out-of-context callbacks never execute code in the target process.
        // Only handles in an explicitly selected target are retained/invalidated.
        sealed class BackgroundLifetime {
            readonly object gate = new object();
            readonly Dictionary<IntPtr, DateTime> watched = new Dictionary<IntPtr, DateTime>();
            readonly HashSet<IntPtr> virtualControls = new HashSet<IntPtr>();
            readonly ManualResetEvent ready = new ManualResetEvent(false);
            readonly AutoResetEvent drained = new AutoResetEvent(false);
            readonly BackgroundEventProc callback;
            long epoch;
            uint threadID;
            volatile bool alive;
            public BackgroundLifetime() {
                callback = OnEvent;
                var thread = new Thread(Listen) { IsBackground = true };
                thread.Start();
                if (!ready.WaitOne(2000) || !alive) Fail("native_unavailable");
            }
            void Listen() {
                IntPtr hook = IntPtr.Zero;
                try {
                    threadID = GetCurrentThreadId();
                    BackgroundMessage message;
                    BackgroundPeekMessage(out message, IntPtr.Zero, 0, 0, 0);
                    // CREATE..VALUECHANGE includes lifecycle, visibility, state,
                    // geometry and name changes. Ignore all untracked windows.
                    hook = SetWinEventHook(0x8000, 0x800E, IntPtr.Zero, callback, 0, 0, 2);
                    alive = hook != IntPtr.Zero;
                    ready.Set();
                    if (!alive) return;
                    while (BackgroundGetMessage(out message, IntPtr.Zero, 0, 0) > 0)
                        if (message.Message == 0x8001) drained.Set();
                } finally {
                    alive = false;
                    ready.Set();
                    drained.Set();
                    if (hook != IntPtr.Zero) UnhookWinEvent(hook);
                    GC.KeepAlive(callback);
                }
            }
            void OnEvent(IntPtr hook, uint eventType, IntPtr hwnd, int objectID, int childID, uint thread, uint time) {
                // Caret/cursor animation and virtual sub-elements are not HWND
                // lifetime/state. Watching them would invalidate every idle frame.
                if (objectID != 0 && objectID != -4) return;
                lock (gate) {
                    if (childID != 0 && !virtualControls.Contains(hwnd)) return;
                    DateTime expires;
                    if (watched.TryGetValue(hwnd, out expires) && expires > DateTime.UtcNow) epoch++;
                }
            }
            public void Watch(IntPtr hwnd) {
                lock (gate) {
                    if (watched.Count >= 8192 && !watched.ContainsKey(hwnd)) Fail("element_limit");
                    watched[hwnd] = DateTime.UtcNow.AddSeconds(35);
                }
            }
            public void WatchBrowser(IntPtr hwnd) {
                Watch(hwnd);
                lock (gate) virtualControls.Add(hwnd);
            }
            public void Prune() {
                lock (gate) {
                    var expired = new List<IntPtr>();
                    DateTime now = DateTime.UtcNow;
                    foreach (var item in watched) if (item.Value <= now) expired.Add(item.Key);
                    foreach (IntPtr hwnd in expired) { watched.Remove(hwnd); virtualControls.Remove(hwnd); }
                }
            }
            public long Read() {
                // The background lane is serial. Drain pending event callbacks on
                // the listener's own queue before trusting a receipt.
                if (!alive || !BackgroundPostThreadMessage(threadID, 0x8001, UIntPtr.Zero, IntPtr.Zero) ||
                    !drained.WaitOne(2000) || !alive) Fail("native_unavailable");
                lock (gate) return epoch;
            }
            public void Reset() {
                lock (gate) { watched.Clear(); virtualControls.Clear(); epoch++; }
            }
        }
        sealed class BackgroundReceipt {
            public string Target, Desktop;
            public BackgroundControl Control;
            public long Epoch;
            public DateTime Expires;
        }
        static BackgroundLifetime backgroundLifetime = null;
        static readonly Dictionary<string, BackgroundReceipt> backgroundReceipts = new Dictionary<string, BackgroundReceipt>();
        static void PrepareBackgroundLifetime(Target target) {
            if (backgroundLifetime == null) backgroundLifetime = new BackgroundLifetime();
            var expired = new List<string>();
            DateTime now = DateTime.UtcNow;
            foreach (var item in backgroundReceipts) if (item.Value.Expires <= now) expired.Add(item.Key);
            foreach (string id in expired) backgroundReceipts.Remove(id);
            PruneBrowserReceipts();
            if (backgroundReceipts.Count == 0 && browserReceipts.Count == 0) backgroundLifetime.Reset();
            backgroundLifetime.Prune();
            backgroundLifetime.Watch(target.Handle);
        }
        static void CheckBackgroundEpoch(long expected) {
            if (backgroundLifetime == null || backgroundLifetime.Read() != expected) Fail("refresh_required");
        }
        static string RememberBackgroundControl(Target target, BackgroundControl control, long epoch) {
            if (backgroundReceipts.Count >= 8192) Fail("element_limit");
            string id = "native:" + Guid.NewGuid().ToString("N");
            backgroundReceipts.Add(id, new BackgroundReceipt { Target = target.ID,
                Desktop = Text(target.Window, "desktop_id"), Control = control, Epoch = epoch,
                Expires = DateTime.UtcNow.AddSeconds(30) });
            return id;
        }
        static BackgroundReceipt TakeBackgroundReceipt(Target target, string id) {
            BackgroundReceipt receipt;
            if (!backgroundReceipts.TryGetValue(id, out receipt)) Fail("refresh_required");
            backgroundReceipts.Remove(id);
            if (receipt.Target != target.ID || receipt.Desktop != Text(target.Window, "desktop_id") ||
                receipt.Expires <= DateTime.UtcNow) Fail("refresh_required");
            CheckBackgroundEpoch(receipt.Epoch);
            return receipt;
        }
    }
}
