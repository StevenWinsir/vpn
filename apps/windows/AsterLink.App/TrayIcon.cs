using System.Drawing;
using System.Drawing.Drawing2D;
using System.Drawing.Text;
using System.Windows.Forms;

namespace AsterLink.App;

/// <summary>
/// The notification-area icon. Closing the window while a tunnel is up leaves AsterLink here; "退出" is the one
/// entry that stops the proxy. The icon is drawn at run time (a state-coloured disc with an "A") so the build
/// needs no binary art asset, and the state is always in the tooltip text too, never colour alone.
/// </summary>
public sealed class TrayIcon : IDisposable
{
    private readonly NotifyIcon icon;
    private readonly ToolStripMenuItem open = new("打开 AsterLink");
    private readonly ToolStripMenuItem toggle = new("连接");
    private readonly ToolStripMenuItem exit = new("退出（停止代理）");
    private readonly Icon idle = Draw(Color.FromArgb(0x6B, 0x76, 0x86));
    private readonly Icon live = Draw(Color.FromArgb(0x1F, 0x7A, 0x4D));
    private readonly Icon bad = Draw(Color.FromArgb(0xB3, 0x26, 0x1E));

    public TrayIcon(Action openWindow, Action toggleConnection, Action exitApp)
    {
        var menu = new ContextMenuStrip();
        menu.Items.Add(open);
        menu.Items.Add(toggle);
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add(exit);
        open.Click += (_, _) => openWindow();
        toggle.Click += (_, _) => toggleConnection();
        exit.Click += (_, _) => exitApp();

        icon = new NotifyIcon { Icon = idle, Text = "AsterLink", ContextMenuStrip = menu, Visible = true };
        icon.MouseClick += (_, e) => { if (e.Button == MouseButtons.Left) openWindow(); };
    }

    /// <summary>Icon of the window and the tray share one drawing.</summary>
    public static Icon WindowIcon() => Draw(Color.FromArgb(0x2F, 0x6F, 0xEB));

    public void Update(string tooltip, string state, bool signedIn, bool canToggle, bool connected)
    {
        // NotifyIcon.Text is limited to 63 characters.
        icon.Text = tooltip.Length > 63 ? tooltip[..63] : tooltip;
        icon.Icon = state switch { "connected" => live, "error" or "blocked" => bad, _ => idle };
        toggle.Text = connected ? "断开" : "连接";
        toggle.Enabled = signedIn && canToggle;
    }

    public void Notify(string title, string text) => icon.ShowBalloonTip(6000, title, text, ToolTipIcon.Info);

    public void Dispose()
    {
        icon.Visible = false;
        icon.Dispose();
        idle.Dispose();
        live.Dispose();
        bad.Dispose();
    }

    private static Icon Draw(Color fill)
    {
        using var bitmap = new Bitmap(32, 32);
        using (var g = Graphics.FromImage(bitmap))
        {
            g.SmoothingMode = SmoothingMode.AntiAlias;
            g.TextRenderingHint = TextRenderingHint.AntiAliasGridFit;
            using var brush = new SolidBrush(fill);
            g.FillEllipse(brush, 1, 1, 30, 30);
            using var font = new Font("Segoe UI", 15f, FontStyle.Bold, GraphicsUnit.Pixel);
            using var format = new StringFormat { Alignment = StringAlignment.Center, LineAlignment = StringAlignment.Center };
            g.DrawString("A", font, Brushes.White, new RectangleF(0, 1, 32, 32), format);
        }
        // GetHicon hands over a native handle; the Icon is cloned so the handle can be released at once.
        var handle = bitmap.GetHicon();
        try { using var temporary = Icon.FromHandle(handle); return (Icon)temporary.Clone(); }
        finally { DestroyIcon(handle); }
    }

    [System.Runtime.InteropServices.DllImport("user32.dll")]
    private static extern bool DestroyIcon(IntPtr handle);
}
