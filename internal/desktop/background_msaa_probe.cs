// Test-only MSAA compatibility probe for owned temporary-profile browsers.
namespace AHADesktop {
    public static partial class Native {
        [DllImport("oleacc.dll")] static extern int AccessibleObjectFromWindow(IntPtr hwnd, uint id,
            ref Guid iid, [MarshalAs(UnmanagedType.Interface)] out Accessibility.IAccessible accessible);
        [DllImport("oleacc.dll")] static extern int AccessibleChildren(Accessibility.IAccessible parent, int start, int count,
            [Out, MarshalAs(UnmanagedType.LPArray, SizeParamIndex = 2)] object[] children, out int obtained);
        sealed class MsaaProbeNode {
            public Accessibility.IAccessible Object;
            public object Child;
            public string ID;
            public int Depth;
        }
        static List<MsaaProbeNode> MsaaProbeNodes(Target target) {
            var queue = new Queue<MsaaProbeNode>();
            var handles = new List<IntPtr> { target.Handle };
            EnumChildWindows(target.Handle, delegate(IntPtr hwnd, IntPtr unused) { handles.Add(hwnd); return true; }, IntPtr.Zero);
            foreach (IntPtr hwnd in handles) {
                Guid iid = new Guid("618736E0-3C3D-11CF-810C-00AA00389B71");
                Accessibility.IAccessible root;
                if (AccessibleObjectFromWindow(hwnd, 0xfffffffc, ref iid, out root) == 0 && root != null)
                    queue.Enqueue(new MsaaProbeNode { Object = root, Child = 0, ID = hwnd.ToInt64().ToString("x"), Depth = 0 });
            }
            var result = new List<MsaaProbeNode>();
            while (queue.Count > 0) {
                var node = queue.Dequeue();
                if (node.Depth > 64 || result.Count >= MaxNodes) Fail("element_limit");
                result.Add(node);
                if (!(node.Child is int) || (int)node.Child != 0) continue;
                int count = node.Object.accChildCount;
                if (count <= 0) continue;
                if (count > MaxNodes || queue.Count + result.Count + count > MaxNodes) Fail("element_limit");
                var children = new object[count];
                int obtained;
                if (AccessibleChildren(node.Object, 0, count, children, out obtained) < 0) continue;
                for (int i = 0; i < obtained; i++) {
                    var accessible = children[i] as Accessibility.IAccessible;
                    if (accessible != null) queue.Enqueue(new MsaaProbeNode { Object = accessible, Child = 0,
                        ID = node.ID + "." + i, Depth = node.Depth + 1 });
                    else if (children[i] is int) queue.Enqueue(new MsaaProbeNode { Object = node.Object, Child = children[i],
                        ID = node.ID + "." + i, Depth = node.Depth + 1 });
                }
            }
            return result;
        }
        static object BrowserMsaaObserve(Target target) {
            var descriptions = new List<object>();
            foreach (var node in MsaaProbeNodes(target)) {
                int role = Convert.ToInt32(node.Object.get_accRole(node.Child), CultureInfo.InvariantCulture);
                int state = Convert.ToInt32(node.Object.get_accState(node.Child), CultureInfo.InvariantCulture);
                bool password = (state & 0x20000000) != 0;
                string name = password ? "" : node.Object.get_accName(node.Child);
                var actions = new List<string>();
                if (!password && (state & 1) == 0) {
                    if (!string.IsNullOrEmpty(node.Object.get_accDefaultAction(node.Child))) actions.Add("invoke");
                    if (role == 42 && (state & 0x40) == 0) actions.Add("set_value");
                }
                descriptions.Add(Obj("id", node.ID, "name", Bounded(name), "role", "MSAA:" + role,
                    "value", "", "actions", actions, "x", 0, "y", 0, "width", 0, "height", 0));
            }
            return Obj("window", target.Window, "elements", descriptions, "image", "", "width", 0, "height", 0);
        }
        static void BrowserMsaaAct(Target target, Dictionary<string, object> action) {
            foreach (var node in MsaaProbeNodes(target)) {
                if (node.ID != Text(action, "element_id")) continue;
                int state = Convert.ToInt32(node.Object.get_accState(node.Child), CultureInfo.InvariantCulture);
                if ((state & 0x20000000) != 0) Fail("password_element");
                target.Revalidate();
                if (Text(action, "kind") == "invoke") node.Object.accDoDefaultAction(node.Child);
                else node.Object.set_accValue(node.Child, Text(action, "value"));
                Thread.Sleep(150);
                target.Revalidate();
                return;
            }
            Fail("element_unavailable");
        }
    }
}
