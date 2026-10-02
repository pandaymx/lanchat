using System.Runtime.InteropServices;
using System.Text;

namespace LANChat.Services;

/// <summary>
/// 运行时判定当前进程是否具备 MSIX 包标识（package identity）。
/// <para>
/// Windows.Storage.ApplicationData、Windows.ApplicationModel.Package.Current、
/// FullTrustProcessLauncher 等 WinRT API 都要求包标识；在 unpackaged 进程
/// （MSI 或便携 zip 安装）里调用会抛 InvalidOperationException，
/// 其默认消息为 "Operation is not valid due to the current state of the object."，
/// 对用户毫无意义。凡是要走这些 API 的分支，必须先用本类型判定。
/// </para>
/// </summary>
internal static class PackageIdentity
{
    /// <summary>kernel32 返回码：进程无包标识（APPMODEL_ERROR_NO_PACKAGE）。</summary>
    private const int AppModelErrorNoPackage = 15700;

    private static readonly Lazy<bool> _isPackaged = new(Detect);

    /// <summary>当前进程是否运行在带包标识的 MSIX 包内。</summary>
    public static bool IsPackaged => _isPackaged.Value;

    private static bool Detect()
    {
        // 传 null 缓冲区探测：有包标识时返回 ERROR_INSUFFICIENT_BUFFER(122)，
        // 无包标识时返回 APPMODEL_ERROR_NO_PACKAGE(15700)。
        uint length = 0;
        return GetCurrentPackageFullName(ref length, null) != AppModelErrorNoPackage;
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern int GetCurrentPackageFullName(
        ref uint packageFullNameLength,
        StringBuilder? packageFullName);
}
