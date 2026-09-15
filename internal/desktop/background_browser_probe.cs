// Test-only probe. Never compiled into the production native worker.
namespace AHADesktop {
    public static partial class Native {
        static List<AutomationElement> BrowserProbeElements(Target target) {
            var result = new List<AutomationElement>();
            var pending = new Stack<KeyValuePair<AutomationElement, int>>();
            var root = AutomationElement.FromHandle(target.Handle);
            pending.Push(new KeyValuePair<AutomationElement, int>(root, 0));
            EnumChildWindows(target.Handle, delegate(IntPtr hwnd, IntPtr unused) {
                if (IsWindowVisible(hwnd))
                    pending.Push(new KeyValuePair<AutomationElement, int>(AutomationElement.FromHandle(hwnd), 0));
                return true;
            }, IntPtr.Zero);
            var walker = TreeWalker.RawViewWalker;
            int nodes = 0;
            var seen = new HashSet<string>();
            while (pending.Count > 0) {
                var item = pending.Pop();
                if (++nodes > MaxNodes || item.Value > 64 || result.Count >= MaxElements) Fail("element_limit");
                var element = item.Key;
                if (!seen.Add(ElementID(element))) continue;
                result.Add(element);
                if (element.Current.IsPassword) continue;
                for (var child = walker.GetFirstChild(element); child != null; child = walker.GetNextSibling(child))
                    pending.Push(new KeyValuePair<AutomationElement, int>(child, item.Value + 1));
            }
            return result;
        }
        static object BrowserProbeObserve(Target target) {
            Rect rect;
            if (!GetWindowRect(target.Handle, out rect)) Fail("stale_target");
            var descriptions = new List<object>();
            foreach (var element in BrowserProbeElements(target)) {
                var description = Describe(element, rect);
                var actions = new List<string>();
                object pattern;
                if (!element.Current.IsPassword && element.Current.IsEnabled) {
                    if (element.TryGetCurrentPattern(InvokePattern.Pattern, out pattern)) actions.Add("invoke");
                    if (element.TryGetCurrentPattern(ValuePattern.Pattern, out pattern) &&
                        !((ValuePattern)pattern).Current.IsReadOnly) actions.Add("set_value");
                }
                description["actions"] = actions;
                descriptions.Add(description);
            }
            return Obj("window", target.Window, "elements", descriptions, "image", "", "width", 0, "height", 0);
        }
        static void BrowserProbeAct(Target target, Dictionary<string, object> action) {
            foreach (var element in BrowserProbeElements(target)) {
                if (ElementID(element) != Text(action, "element_id")) continue;
                if (element.Current.IsPassword) Fail("password_element");
                target.Revalidate();
                if (Text(action, "kind") == "invoke") ((InvokePattern)element.GetCurrentPattern(InvokePattern.Pattern)).Invoke();
                else ((ValuePattern)element.GetCurrentPattern(ValuePattern.Pattern)).SetValue(Text(action, "value"));
                Thread.Sleep(150);
                target.Revalidate();
                return;
            }
            Fail("element_unavailable");
        }
    }
}
