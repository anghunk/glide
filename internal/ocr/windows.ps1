$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
if ($env:GLIDE_OCR_MODE -eq 'capture') {
    Add-Type -AssemblyName System.Windows.Forms, System.Drawing
    Add-Type @'
using System;
using System.Drawing;
using System.Drawing.Imaging;
using System.Windows.Forms;
public sealed class GlideCapture : Form {
    private readonly Bitmap screen;
    private Point start, end;
    private bool dragging;
    public Rectangle Selection { get; private set; }
    public GlideCapture() {
        var bounds = SystemInformation.VirtualScreen;
        screen = new Bitmap(bounds.Width, bounds.Height);
        using (var graphics = Graphics.FromImage(screen)) {
            graphics.CopyFromScreen(bounds.Location, Point.Empty, bounds.Size);
        }
        FormBorderStyle = FormBorderStyle.None;
        StartPosition = FormStartPosition.Manual;
        Bounds = bounds;
        TopMost = true;
        DoubleBuffered = true;
        KeyPreview = true;
        Cursor = Cursors.Cross;
        ShowInTaskbar = false;
    }
    private Rectangle RegionRect() {
        return Rectangle.FromLTRB(Math.Min(start.X,end.X),Math.Min(start.Y,end.Y),Math.Max(start.X,end.X),Math.Max(start.Y,end.Y));
    }
    protected override void OnPaint(PaintEventArgs e) {
        e.Graphics.DrawImageUnscaled(screen, Point.Empty);
        using (var shade = new SolidBrush(Color.FromArgb(95,0,0,0))) e.Graphics.FillRectangle(shade, ClientRectangle);
        if (dragging) {
            var rect = RegionRect();
            if (rect.Width > 0 && rect.Height > 0) {
                e.Graphics.DrawImage(screen,rect,rect,GraphicsUnit.Pixel);
                using (var pen = new Pen(Color.DodgerBlue,2)) e.Graphics.DrawRectangle(pen,rect);
            }
        }
    }
    protected override void OnMouseDown(MouseEventArgs e) {
        if (e.Button == MouseButtons.Right) { DialogResult=DialogResult.Cancel; Close(); return; }
        if (e.Button != MouseButtons.Left) return;
        start=end=e.Location; dragging=true; Capture=true;
    }
    protected override void OnMouseMove(MouseEventArgs e) {
        if (!dragging) return;
        end=new Point(Math.Max(0,Math.Min(ClientSize.Width,e.X)),Math.Max(0,Math.Min(ClientSize.Height,e.Y)));
        Invalidate();
    }
    protected override void OnMouseUp(MouseEventArgs e) {
        if (!dragging || e.Button != MouseButtons.Left) return;
        Selection=RegionRect(); dragging=false; Capture=false;
        DialogResult=Selection.Width>2 && Selection.Height>2 ? DialogResult.OK : DialogResult.Cancel;
        Close();
    }
    protected override void OnKeyDown(KeyEventArgs e) {
        if (e.KeyCode==Keys.Escape) { DialogResult=DialogResult.Cancel; Close(); }
    }
    public void SaveCapture(string path) {
        using (var cropped=screen.Clone(Selection,PixelFormat.Format32bppArgb)) cropped.Save(path,ImageFormat.Png);
    }
    protected override void Dispose(bool disposing) {
        if(disposing) screen.Dispose();
        base.Dispose(disposing);
    }
}
'@
    # 保持物理像素坐标，兼容高 DPI 和多显示器。
    Add-Type @'
using System.Runtime.InteropServices;
public static class GlideDpi {
    [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
}
'@
    [GlideDpi]::SetProcessDPIAware() | Out-Null
    $form = New-Object GlideCapture
    try {
        if ($form.ShowDialog() -ne [System.Windows.Forms.DialogResult]::OK) { exit 2 }
        $form.SaveCapture($env:GLIDE_OCR_PATH)
    } finally { $form.Dispose() }
    exit 0
}
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$null = [Windows.Storage.StorageFile, Windows.Storage, ContentType=WindowsRuntime]
$null = [Windows.Storage.Streams.IRandomAccessStream, Windows.Storage.Streams, ContentType=WindowsRuntime]
$null = [Windows.Graphics.Imaging.BitmapDecoder, Windows.Graphics.Imaging, ContentType=WindowsRuntime]
$null = [Windows.Graphics.Imaging.SoftwareBitmap, Windows.Graphics.Imaging, ContentType=WindowsRuntime]
$null = [Windows.Media.Ocr.OcrEngine, Windows.Foundation, ContentType=WindowsRuntime]
$null = [Windows.Media.Ocr.OcrResult, Windows.Foundation, ContentType=WindowsRuntime]
$asTask = [System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
    $_.Name -eq 'AsTask' -and $_.IsGenericMethod -and $_.GetGenericArguments().Count -eq 1 -and
    $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1'
} | Select-Object -First 1
function Wait-OcrOperation($operation, $type) {
    $task = $asTask.MakeGenericMethod($type).Invoke($null, @($operation))
    $task.GetAwaiter().GetResult()
}
$file = Wait-OcrOperation ([Windows.Storage.StorageFile]::GetFileFromPathAsync($env:GLIDE_OCR_PATH)) ([Windows.Storage.StorageFile])
$stream = Wait-OcrOperation ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) ([Windows.Storage.Streams.IRandomAccessStream])
try {
    $decoder = Wait-OcrOperation ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
    $transform = [Windows.Graphics.Imaging.BitmapTransform, Windows.Graphics.Imaging, ContentType=WindowsRuntime]::new()
    $limit = [Windows.Media.Ocr.OcrEngine]::MaxImageDimension
    $ratio = [Math]::Min(1.0, $limit / [double][Math]::Max($decoder.PixelWidth, $decoder.PixelHeight))
    $transform.ScaledWidth = [uint32][Math]::Max(1, [Math]::Floor($decoder.PixelWidth * $ratio))
    $transform.ScaledHeight = [uint32][Math]::Max(1, [Math]::Floor($decoder.PixelHeight * $ratio))
    $bitmap = Wait-OcrOperation ($decoder.GetSoftwareBitmapAsync(
        [Windows.Graphics.Imaging.BitmapPixelFormat]::Gray8,
        [Windows.Graphics.Imaging.BitmapAlphaMode]::Ignore, $transform,
        [Windows.Graphics.Imaging.ExifOrientationMode]::RespectExifOrientation,
        [Windows.Graphics.Imaging.ColorManagementMode]::DoNotColorManage)) ([Windows.Graphics.Imaging.SoftwareBitmap])
    try {
        $engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromUserProfileLanguages()
        if ($null -eq $engine) { throw 'No installed OCR language' }
        $result = Wait-OcrOperation ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])
        [Console]::Write(($result.Lines | ForEach-Object { $_.Text }) -join "`n")
    } finally { $bitmap.Dispose() }
} finally { $stream.Dispose() }
