namespace AHADesktop {
    public static partial class Native {
        static readonly ImageCodecInfo JPEGCodec = FindJPEGCodec();
        static ImageCodecInfo FindJPEGCodec() {
            foreach (ImageCodecInfo codec in ImageCodecInfo.GetImageEncoders())
                if (codec.FormatID == ImageFormat.Jpeg.Guid) return codec;
            return null;
        }
        static string CapturePreview(Surface surface, int maxWidth, int maxHeight, int quality,
            out int previewWidth, out int previewHeight, out string error) {
            error = "";
            previewWidth = previewHeight = 0;
            long width = (long)surface.Bounds.Right - surface.Bounds.Left;
            long height = (long)surface.Bounds.Bottom - surface.Bounds.Top;
            if (!surface.Desktop && IsIconic(surface.Target.Handle)) { error = "window_minimized"; return ""; }
            if (width <= 0 || height <= 0 || width > 8192 || height > 8192 || width * height > MaxPixels) {
                error = "capture_size_limit"; return "";
            }
            double scale = Math.Min(1.0, Math.Min(maxWidth / (double)width, maxHeight / (double)height));
            previewWidth = Math.Max(1, (int)Math.Floor(width * scale));
            previewHeight = Math.Max(1, (int)Math.Floor(height * scale));
            bool physical = !surface.Desktop && ForegroundUnobscured(surface);
            try {
                using (var original = new Bitmap((int)width, (int)height, PixelFormat.Format32bppArgb)) {
                    using (Graphics graphics = Graphics.FromImage(original)) {
                        if (surface.Desktop || physical)
                            graphics.CopyFromScreen(surface.Bounds.Left, surface.Bounds.Top, 0, 0,
                                new Size((int)width, (int)height), CopyPixelOperation.SourceCopy);
                        else {
                            IntPtr dc = graphics.GetHdc();
                            try {
                                if (!PrintWindow(surface.Target.Handle, dc, 2)) { error = "window_capture_unavailable"; return ""; }
                            } finally { graphics.ReleaseHdc(dc); }
                        }
                    }
                    if (physical && !ForegroundUnobscured(surface)) { error = "window_capture_obscured"; return ""; }
                    if (!surface.Desktop && !physical) {
                        bool varied = false;
                        int first = original.GetPixel(0, 0).ToArgb();
                        for (int y = 0; y < height && !varied; y += Math.Max(1, (int)height / 32))
                            for (int x = 0; x < width && !varied; x += Math.Max(1, (int)width / 32))
                                varied = original.GetPixel(x, y).ToArgb() != first;
                        if (!varied) { error = "window_capture_blank"; return ""; }
                    }
                    using (var scaled = new Bitmap(previewWidth, previewHeight, PixelFormat.Format24bppRgb)) {
                        using (Graphics graphics = Graphics.FromImage(scaled)) {
                            graphics.InterpolationMode = System.Drawing.Drawing2D.InterpolationMode.HighQualityBilinear;
                            graphics.PixelOffsetMode = System.Drawing.Drawing2D.PixelOffsetMode.HighQuality;
                            graphics.DrawImage(original, new Rectangle(0, 0, previewWidth, previewHeight),
                                new Rectangle(0, 0, (int)width, (int)height), GraphicsUnit.Pixel);
                        }
                        using (var parameters = new EncoderParameters(1))
                        using (var stream = new MemoryStream()) {
                            parameters.Param[0] = new EncoderParameter(System.Drawing.Imaging.Encoder.Quality, (long)quality);
                            scaled.Save(stream, JPEGCodec, parameters);
                            if (stream.Length > 6 * 1024 * 1024) { error = "capture_size_limit"; return ""; }
                            return Convert.ToBase64String(stream.ToArray());
                        }
                    }
                }
            } catch { error = "window_capture_unavailable"; return ""; }
        }
        public static string RunPreview(string requestJSON) {
            try {
                if (SetThreadDpiAwarenessContext(new IntPtr(-4)) == IntPtr.Zero) Fail("native_unavailable");
                var request = JSON.Deserialize<Dictionary<string, object>>(requestJSON);
                var frame = request["frame"] as Dictionary<string, object>;
                if (frame == null) Fail("invalid_request");
                int maxWidth = (int)Number(frame, "max_width"), maxHeight = (int)Number(frame, "max_height"),
                    quality = (int)Number(frame, "quality");
                if (maxWidth < 320 || maxWidth > 1920 || maxHeight < 180 || maxHeight > 1080 || quality < 35 || quality > 85)
                    Fail("invalid_request");
                string id = Text(request, "window_id");
                Surface surface = ReadSurface(id, true);
                int previewWidth, previewHeight;
                string error;
                string image = CapturePreview(surface, maxWidth, maxHeight, quality, out previewWidth, out previewHeight, out error);
                Surface after = ReadSurface(id, true);
                ReconcileCapture(surface, after, ref image, ref error);
                var observation = Obj("window", surface.Window, "elements", new object[0], "image", image,
                    "width", Math.Max(0L, (long)surface.Bounds.Right - surface.Bounds.Left),
                    "height", Math.Max(0L, (long)surface.Bounds.Bottom - surface.Bounds.Top),
                    "surface", surface.Identity, "capture_error", error, "mime", "image/jpeg",
                    "preview_width", previewWidth, "preview_height", previewHeight,
                    "control_error", surface.ControlError, "input_actions", CaptureActions(surface, image));
                return JSON.Serialize(Obj("ok", true, "observation", observation));
            } catch (NativeFailure ex) { return JSON.Serialize(Obj("ok", false, "error", ex.Code)); }
            catch { return JSON.Serialize(Obj("ok", false, "error", "native_unavailable")); }
        }
    }
}
