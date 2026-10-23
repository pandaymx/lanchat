using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.Input;
using LANChat.Models;
using LANChat.Services;

namespace LANChat.ViewModels;

/// <summary>传输列表：进度更新、完成/失败标记、取消。</summary>
public sealed partial class TransfersViewModel : ViewModelBase
{
    private readonly AppServices _services;

    public ObservableCollection<Transfer> Transfers { get; } = [];

    public TransfersViewModel(AppServices services, UiThread ui)
    {
        _services = services;
    }

    public void ReplaceTransfers(IEnumerable<Transfer> transfers)
    {
        var matrixById = Transfers
            .Where(t => !string.IsNullOrEmpty(t.MatrixSummary))
            .ToDictionary(t => t.Id, t => t.MatrixSummary);
        Transfers.Clear();
        foreach (var transfer in transfers)
        {
            if (matrixById.TryGetValue(transfer.Id, out var summary))
            {
                Transfers.Add(transfer with { MatrixSummary = summary });
            }
            else
            {
                Transfers.Add(transfer);
            }
        }
    }

    /// <summary>按 id upsert 进度快照。</summary>
    public void Upsert(Transfer transfer)
    {
        var index = -1;
        for (var i = 0; i < Transfers.Count; i++)
        {
            if (Transfers[i].Id == transfer.Id)
            {
                index = i;
                break;
            }
        }

        if (index >= 0)
        {
            var prevMatrix = Transfers[index].MatrixSummary;
            Transfers[index] = string.IsNullOrEmpty(transfer.MatrixSummary) && !string.IsNullOrEmpty(prevMatrix)
                ? transfer with { MatrixSummary = prevMatrix }
                : transfer;
        }
        else
        {
            Transfers.Add(transfer);
        }
    }

    public void MarkDone(string transferId)
    {
        var existing = Find(transferId);
        if (existing is not null)
        {
            Transfers[Transfers.IndexOf(existing)] = existing with
            {
                State = TransferState.Done,
                BytesDone = existing.Size,
            };
        }
    }

    public void MarkFailed(string transferId, string reason)
    {
        var existing = Find(transferId);
        if (existing is not null)
        {
            Transfers[Transfers.IndexOf(existing)] = existing with
            {
                State = TransferState.Failed,
                ErrorReason = reason,
            };
        }
    }

    public void ApplyGroupMatrix(string transferId, string haveBitmapB64)
    {
        var existing = Find(transferId);
        if (existing is null)
        {
            return;
        }

        var summary = Transfer.FormatMatrixSummary(haveBitmapB64, existing.Size);
        if (!string.IsNullOrEmpty(summary))
        {
            Transfers[Transfers.IndexOf(existing)] = existing with
            {
                MatrixSummary = summary,
            };
        }
    }

    [RelayCommand]
    private async Task PauseAsync(Transfer? transfer)
    {
        if (transfer is null)
        {
            return;
        }
        try
        {
            await _services.Ipc.InvokeAsync("PauseFile", new
            {
                transferID = transfer.Id,
            });
        }
        catch
        {
            // 暂停失败时保留列表状态
        }
    }

    [RelayCommand]
    private async Task ResumeAsync(Transfer? transfer)
    {
        if (transfer is null)
        {
            return;
        }
        try
        {
            await _services.Ipc.InvokeAsync("ResumeFile", new
            {
                transferID = transfer.Id,
            });
        }
        catch
        {
            // 恢复失败时保留列表状态
        }
    }

    [RelayCommand]
    private async Task CancelAsync(Transfer? transfer)
    {
        if (transfer is null)
        {
            return;
        }
        try
        {
            await _services.Ipc.InvokeAsync("CancelFile", new
            {
                transferID = transfer.Id,
            });
        }
        catch
        {
            // 取消失败时保留列表状态，下次进度通知纠正
        }
    }

    private Transfer? Find(string id) =>
        Transfers.FirstOrDefault(t => t.Id == id);
}
