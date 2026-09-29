using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Data;
using Microsoft.UI.Xaml.Media;
using Windows.UI;

namespace LANChat.Converters;

/// <summary>bool(IsSelf) → HorizontalAlignment：自己靠右，对方靠左。</summary>
public sealed class SelfToHorizontalAlignmentConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language) =>
        value is true ? HorizontalAlignment.Right : HorizontalAlignment.Left;

    public object ConvertBack(object value, Type targetType, object parameter, string language) =>
        throw new NotSupportedException();
}

/// <summary>bool(IsSelf) → TextAlignment。</summary>
public sealed class SelfToTextAlignmentConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language) =>
        value is true ? TextAlignment.Right : TextAlignment.Left;

    public object ConvertBack(object value, Type targetType, object parameter, string language) =>
        throw new NotSupportedException();
}

/// <summary>bool(IsSelf) → 气泡背景色。</summary>
public sealed class BubbleColorConverter : IValueConverter
{
    private static readonly Color SelfColor = Color.FromArgb(255, 149, 211, 102);   // QQ 绿
    private static readonly Color OtherColor = Color.FromArgb(255, 245, 245, 245);

    public object Convert(object value, Type targetType, object parameter, string language) =>
        new SolidColorBrush(value is true ? SelfColor : OtherColor);

    public object ConvertBack(object value, Type targetType, object parameter, string language) =>
        throw new NotSupportedException();
}
