package ru.buffer;

import android.app.Activity;
import android.app.AlertDialog;
import android.app.StatusBarManager;
import android.content.ComponentName;
import android.content.Intent;
import android.graphics.drawable.Icon;
import android.os.Build;
import android.os.Bundle;
import android.view.View;
import android.widget.*;

public class Main extends Activity {
    private TextView status, pairBadge;
    private ProgressBar progress;
    private Button send, pull, clipboardSend;
    private String shared;
    private boolean sending;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        setContentView(R.layout.activity_main);
        status = findViewById(R.id.status);
        pairBadge = findViewById(R.id.pair_badge);
        progress = findViewById(R.id.main_progress);
        findViewById(R.id.scan).setOnClickListener(v -> startActivityForResult(new Intent(this, Scan.class), 1));
        send = findViewById(R.id.share_send);
        send.setOnClickListener(v -> transmit());
        clipboardSend = findViewById(R.id.clipboard_send);
        clipboardSend.setOnClickListener(v -> sendClipboard());
        pull = findViewById(R.id.pull);
        pull.setOnClickListener(v -> transfer(null));
        if (Build.VERSION.SDK_INT >= 33) {
            View addTile = findViewById(R.id.add_tile);
            addTile.setVisibility(View.VISIBLE);
            addTile.setOnClickListener(v -> chooseTile());
        }
        Intent i = getIntent();
        if (Intent.ACTION_SEND.equals(i.getAction()) && i.getType() != null && i.getType().startsWith("text/")) {
            CharSequence text = i.getCharSequenceExtra(Intent.EXTRA_TEXT);
            if (text != null) shared = text.toString();
            else shared = Transfer.textFrom(i.getClipData());
        }
        send.setVisibility(shared == null ? View.GONE : View.VISIBLE);
        boolean paired = Pair.file(this).getBaseFile().exists();
        showPairing(paired);
        status.setText(paired ? "Скопируй текст на любом из устройств и выбери направление." : "Для привязки запусти buffer.exe на ПК.");
        if (shared != null && paired && state == null) transmit();
        else if (state != null) status.setText(state.getString("status", "Готово"));
    }

    @Override public void onSaveInstanceState(Bundle b) {
        super.onSaveInstanceState(b);
        b.putString("status", sending ? "Проверь буфер перед повторной операцией." : status.getText().toString());
    }

    @Override public void onActivityResult(int req, int result, Intent data) {
        super.onActivityResult(req,result,data);
        if (req == 1 && result == RESULT_OK && data != null) {
            try {
                Pair.save(this, data.getStringExtra("qr"));
                showPairing(true);
                status.setText("Компьютер привязан.");
                if (shared != null) transmit();
            } catch (Exception e) {
                status.setText("QR не подходит или ключ не удалось сохранить.");
            }
        }
    }

    private void showPairing(boolean paired) {
        pairBadge.setText(paired ? R.string.paired_badge : R.string.unpaired_badge);
    }

    private void chooseTile() {
        if (Build.VERSION.SDK_INT < 33) return;
        CharSequence[] labels = {getText(R.string.tile_send), getText(R.string.tile_pull)};
        new AlertDialog.Builder(this).setTitle("Добавить плитку в быстрые настройки")
            .setItems(labels, (dialog, index) -> {
                Class<?> tile = index == 0 ? ClipboardTile.Send.class : ClipboardTile.Pull.class;
                int icon = index == 0 ? R.drawable.ic_tile_send : R.drawable.ic_tile_pull;
                getSystemService(StatusBarManager.class).requestAddTileService(new ComponentName(this, tile),
                    labels[index], Icon.createWithResource(this, icon), getMainExecutor(), result -> {
                        if (isFinishing() || isDestroyed()) return;
                        if (result == StatusBarManager.TILE_ADD_REQUEST_RESULT_TILE_ADDED) {
                            Toast.makeText(this, "Плитка добавлена", Toast.LENGTH_SHORT).show();
                        } else if (result == StatusBarManager.TILE_ADD_REQUEST_RESULT_TILE_ALREADY_ADDED) {
                            Toast.makeText(this, "Плитка уже добавлена", Toast.LENGTH_SHORT).show();
                        } else if (result != StatusBarManager.TILE_ADD_REQUEST_RESULT_TILE_NOT_ADDED) {
                            Toast.makeText(this, R.string.tiles_hint, Toast.LENGTH_LONG).show();
                        }
                    });
            }).show();
    }

    private void sendClipboard() {
        if (sending) return;
        String value = Transfer.readClipboard(this);
        if (value == null) { status.setText("В буфере телефона нет текста или ссылки."); return; }
        transfer(value);
    }

    private void transmit() { if (shared != null) transfer(shared); }

    private void transfer(String value) {
        if (sending) return;
        if (!Pair.file(this).getBaseFile().exists()) { status.setText("Сначала привяжи компьютер по QR."); return; }
        if (!Transfer.valid(value)) { status.setText(Transfer.INVALID_TEXT); return; }
        setBusy(true);
        status.setText(Transfer.progress(value));
        Transfer.run(this, value, result -> {
            setBusy(false);
            status.setText(result.message);
        });
    }

    private void setBusy(boolean busy) {
        sending = busy;
        send.setEnabled(!busy);
        pull.setEnabled(!busy);
        clipboardSend.setEnabled(!busy);
        progress.setVisibility(busy ? View.VISIBLE : View.GONE);
    }
}
