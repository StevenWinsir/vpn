using System.Windows;
using System.Windows.Controls;

namespace AsterLink.App.Controls;

/// <summary>
/// Equal-width columns that wrap: as many columns as fit at <see cref="MinItemWidth"/> (never more than there are
/// children), each row as tall as its tallest card. Three information cards sit side by side in the default window
/// and fall into two rows at the minimum size, without ever scrolling sideways (handoff 6.1).
/// </summary>
public sealed class CardGrid : Panel
{
    public static readonly DependencyProperty MinItemWidthProperty = DependencyProperty.Register(nameof(MinItemWidth), typeof(double), typeof(CardGrid),
        new FrameworkPropertyMetadata(220.0, FrameworkPropertyMetadataOptions.AffectsMeasure));

    public static readonly DependencyProperty GapProperty = DependencyProperty.Register(nameof(Gap), typeof(double), typeof(CardGrid),
        new FrameworkPropertyMetadata(16.0, FrameworkPropertyMetadataOptions.AffectsMeasure));

    public double MinItemWidth { get => (double)GetValue(MinItemWidthProperty); set => SetValue(MinItemWidthProperty, value); }
    public double Gap { get => (double)GetValue(GapProperty); set => SetValue(GapProperty, value); }

    private List<UIElement> Visible() => [.. InternalChildren.Cast<UIElement>().Where(c => c.Visibility != Visibility.Collapsed)];

    private int Columns(double width, int count) =>
        double.IsInfinity(width) ? Math.Max(1, count) : Math.Clamp((int)Math.Floor((width + Gap) / (MinItemWidth + Gap)), 1, Math.Max(1, count));

    protected override Size MeasureOverride(Size available)
    {
        var children = Visible();
        foreach (var hidden in InternalChildren.Cast<UIElement>().Where(c => c.Visibility == Visibility.Collapsed)) hidden.Measure(new Size(0, 0));
        if (children.Count == 0) return new Size(0, 0);
        var width = double.IsInfinity(available.Width) ? children.Count * MinItemWidth + (children.Count - 1) * Gap : available.Width;
        var columns = Columns(width, children.Count);
        var cell = (width - (columns - 1) * Gap) / columns;
        double height = 0;
        for (var start = 0; start < children.Count; start += columns)
        {
            double row = 0;
            foreach (var child in children.Skip(start).Take(columns))
            {
                child.Measure(new Size(cell, double.PositiveInfinity));
                row = Math.Max(row, child.DesiredSize.Height);
            }
            height += row + (start > 0 ? Gap : 0);
        }
        return new Size(width, height);
    }

    protected override Size ArrangeOverride(Size final)
    {
        var children = Visible();
        if (children.Count == 0) return final;
        var columns = Columns(final.Width, children.Count);
        var cell = (final.Width - (columns - 1) * Gap) / columns;
        double y = 0;
        for (var start = 0; start < children.Count; start += columns)
        {
            var row = children.Skip(start).Take(columns).ToList();
            var height = row.Max(c => c.DesiredSize.Height);
            for (var i = 0; i < row.Count; i++) row[i].Arrange(new Rect(i * (cell + Gap), y, cell, height));
            y += height + Gap;
        }
        return final;
    }
}
