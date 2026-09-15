namespace AHADesktop {
    public static partial class Native {
        static string WorkerLine() {
            var line = new StringBuilder();
            int c;
            while ((c = Console.In.Read()) != -1) {
                if (c == '\n') return line.ToString();
                if (line.Length >= 131072) throw new InvalidOperationException();
                if (c != '\r') line.Append((char)c);
            }
            return line.Length == 0 ? null : line.ToString();
        }
        public static void Serve(string lane) {
            if (lane != "capture" && lane != "action" && lane != "background") throw new InvalidOperationException();
            Console.Out.WriteLine("{\"id\":0,\"result\":{\"ok\":true}}");
            Console.Out.Flush();
            long previous = 0;
            string line;
            while ((line = WorkerLine()) != null) {
                var envelope = JSON.Deserialize<Dictionary<string, object>>(line);
                long id = Convert.ToInt64(envelope["id"], CultureInfo.InvariantCulture);
                if (id <= previous) throw new InvalidOperationException();
                previous = id;
                var request = envelope["request"] as Dictionary<string, object>;
                if (request == null) throw new InvalidOperationException();
                string operation = Text(request, "operation");
                bool capture = operation == "windows" || operation == "observe" ||
                    operation == "foreground_observe" || operation == "foreground_preview" || operation == "targets";
                bool action = operation == "act" || operation == "foreground_act" || operation == "foreground_new_desktop" ||
                    operation == "target_select";
                bool background = operation == "background_select" || operation == "background_observe" || operation == "background_act";
                string result;
                if (lane == "capture" && !capture || lane == "action" && !action || lane == "background" && !background)
                    result = "{\"ok\":false,\"error\":\"invalid_request\"}";
                else {
                    NativeInputRequestID = id;
                    string requestJSON = JSON.Serialize(request);
                    if (operation == "targets" || operation == "target_select") result = RunTargets(requestJSON);
                    else if (operation.StartsWith("background_", StringComparison.Ordinal)) result = RunBackground(requestJSON);
                    else if (operation == "foreground_preview") result = RunPreview(requestJSON);
                    else if (operation.StartsWith("foreground_", StringComparison.Ordinal)) result = RunForeground(requestJSON);
                    else result = Run(requestJSON);
                }
                if (result.Length > 12 * 1024 * 1024 - 256) throw new InvalidOperationException();
                Console.Out.Write("{\"id\":" + id.ToString(CultureInfo.InvariantCulture) + ",\"result\":");
                Console.Out.Write(result);
                Console.Out.WriteLine("}");
                Console.Out.Flush();
            }
        }
    }
}
