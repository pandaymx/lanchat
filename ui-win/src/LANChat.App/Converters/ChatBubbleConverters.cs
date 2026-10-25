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

/// <summary>bool(IsSelf) → NTQQ 气泡背景色：发出消息为 QQ 蓝 (#0099FF)，接收消息为纯白卡片 (#FFFFFF)。</summary>
public sealed class BubbleColorConverter : IValueConverter
{
    private static readonly Color SelfColor = Color.FromArgb(255, 0, 153, 255);    // QQ Primary Blue #0099FF
    private static readonly Color OtherColor = Color.FromArgb(255, 255, 255, 255); // White Card Bubble #FFFFFF

    public object Convert(object value, Type targetType, object parameter, string language)
    {
        var color = value is true ? SelfColor : OtherColor;
        return targetType == typeof(Color) ? color : new SolidColorBrush(color);
    }

    public object ConvertBack(object value, Type targetType, object parameter, string language) =>
        throw new NotSupportedException();
}

/// <summary>bool(IsSelf) → NTQQ 气泡文字前景色：发出消息白字 (#FFFFFF)，接收消息深色字 (#1D2129)。</summary>
public sealed class BubbleForegroundConverter : IValueConverter
{
    private static readonly Color SelfForeground = Color.FromArgb(255, 255, 255, 255);
    private static readonly Color OtherForeground = Color.FromArgb(255, 29, 33, 41);

    public object Convert(object value, Type targetType, object parameter, string language)
    {
        var color = value is true ? SelfForeground : OtherForeground;
        return targetType == typeof(Color) ? color : new SolidColorBrush(color);
    }

    public object ConvertBack(object value, Type targetType, object parameter, string language) =>
        throw new NotSupportedException();
}
