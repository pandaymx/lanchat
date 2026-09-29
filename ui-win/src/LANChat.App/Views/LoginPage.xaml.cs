using LANChat.ViewModels;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;

namespace LANChat.Views;

/// <summary>登录页：PasswordBox 无依赖属性绑定，在代码后置同步 PSK。</summary>
public sealed partial class LoginPage : Page
{
    public LoginPage()
    {
        InitializeComponent();
    }

    protected override void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        DataContext = e.Parameter;
    }

    private void PskBox_PasswordChanged(object sender, Microsoft.UI.Xaml.RoutedEventArgs e)
    {
        if (DataContext is ShellViewModel shell)
        {
            shell.Login.Psk = PskBox.Password;
        }
    }
}
