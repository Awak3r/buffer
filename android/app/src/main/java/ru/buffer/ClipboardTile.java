package ru.buffer;

import android.app.PendingIntent;
import android.content.Intent;
import android.os.Build;
import android.service.quicksettings.Tile;
import android.service.quicksettings.TileService;

public abstract class ClipboardTile extends TileService {
    public static final class Send extends ClipboardTile {
        @Override String action() { return QuickTransfer.SEND; }
    }

    public static final class Pull extends ClipboardTile {
        @Override String action() { return QuickTransfer.PULL; }
    }

    abstract String action();

    @Override public void onStartListening() {
        Tile tile = getQsTile();
        if (tile != null) {
            tile.setState(Tile.STATE_INACTIVE);
            tile.updateTile();
        }
    }

    @Override public void onClick() {
        if (isLocked()) unlockAndRun(this::openTransfer);
        else openTransfer();
    }

    @SuppressWarnings("deprecation")
    private void openTransfer() {
        Intent intent = new Intent(this, QuickTransfer.class)
            .setAction(action()).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        if (Build.VERSION.SDK_INT >= 34) {
            startActivityAndCollapse(PendingIntent.getActivity(this, 0, intent, PendingIntent.FLAG_IMMUTABLE));
        } else {
            startActivityAndCollapse(intent);
        }
    }
}
