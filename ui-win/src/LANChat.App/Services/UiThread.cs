using Microsoft.UI.Dispatching;

namespace LANChat.Services;

/// <summary>把后台线程的回调编组到 UI 线程执行。</summary>
public sealed class UiThread
{
    private readonly DispatcherQueue _dispatcher;

    public UiThread(DispatcherQueue dispatcher)
    {
        _dispatcher = dispatcher;
    }

    public void Post(Action action)
    {
        if (_dispatcher.HasThreadAccess)
        {
            action();
        }
        else
        {
            _dispatcher.TryEnqueue(new DispatcherQueueHandler(action));
        }
    }
}
