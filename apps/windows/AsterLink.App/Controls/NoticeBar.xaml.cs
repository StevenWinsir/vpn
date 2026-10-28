using System.Windows;
using System.Windows.Automation;
using System.Windows.Controls;
using System.Windows.Input;

namespace AsterLink.App.Controls;

/// <summary>A tinted line above a page: warning (amber), danger (red) or info (brand), with an optional single action.</summary>
public partial class NoticeBar : UserControl
{
    public static readonly DependencyProperty TextProperty = DependencyProperty.Register(nameof(Text), typeof(string), typeof(NoticeBar),
        new PropertyMetadata("", (d, _) => ((NoticeBar)d).Refresh()));
    public static readonly DependencyProperty KindProperty = DependencyProperty.Register(nameof(Kind), typeof(string), typeof(NoticeBar),
        new PropertyMetadata("warning", (d, _) => ((NoticeBar)d).Refresh()));
    public static readonly DependencyProperty ActionTextProperty = DependencyProperty.Register(nameof(ActionText), typeof(string), typeof(NoticeBar),
        new PropertyMetadata("", (d, _) => ((NoticeBar)d).Refresh()));
    public static readonly DependencyProperty CommandProperty = DependencyProperty.Register(nameof(Command), typeof(ICommand), typeof(NoticeBar));

    public NoticeBar()
    {
        InitializeComponent();
        Refresh();
    }

    public string Text { get => (string)GetValue(TextProperty); set => SetValue(TextProperty, value); }
    public string Kind { get => (string)GetValue(KindProperty); set => SetValue(KindProperty, value); }
    public string ActionText { get => (string)GetValue(ActionTextProperty); set => SetValue(ActionTextProperty, value); }
    public ICommand? Command { get => (ICommand?)GetValue(CommandProperty); set => SetValue(CommandProperty, value); }

    private void Refresh()
    {
        if (Frame is null) return;
        var (background, border, text, icon) = Kind switch
        {
            "danger" => ("DangerBackgroundBrush", "DangerBackgroundBrush", "DangerTextBrush", "IconAlertCircle"),
            "info" => ("BrandSoftBrush", "BrandSoftBrush", "BrandBrush", "IconInfoCircle"),
            _ => ("WarnBackgroundBrush", "WarnBorderBrush", "WarnTextBrush", "IconAlertTriangle"),
        };
        Frame.SetResourceReference(Border.BackgroundProperty, background);
        Frame.SetResourceReference(Border.BorderBrushProperty, border);
        Message.SetResourceReference(TextBlock.ForegroundProperty, text);
        Glyph.SetResourceReference(ForegroundProperty, text);
        Glyph.Tag = TryFindResource(icon) ?? Application.Current?.TryFindResource(icon);
        Action.Visibility = string.IsNullOrEmpty(ActionText) ? Visibility.Collapsed : Visibility.Visible;
        Visibility = string.IsNullOrEmpty(Text) ? Visibility.Collapsed : Visibility.Visible;
        AutomationProperties.SetName(this, Text);
    }
}
