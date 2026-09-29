using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace LANChat.Views;

/// <summary>联系人列表；底部按钮切换设置页。</summary>
public sealed partial class RosterView : UserControl
{
    public RosterView()
    {
        InitializeComponent();
    }

    private void SettingsButton_Click(object sender, RoutedEventArgs e)
    {
        if (DataContext is ViewModels.ShellViewModel shell)
        {
            shell.ShowSettings = true;
        }
    }
}
