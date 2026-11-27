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
    private TextView status;
    private Button send, pull, clipboardSend;
    private String shared;
    private boolean sending;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        LinearLayout view = new LinearLayout(this);
        view.setOrientation(LinearLayout.VERTICAL);
        int pad = (int)(20 * getResources().getDisplayMetrics().density);
        view.setPadding(pad,pad,pad,pad);
        status = new TextView(this); status.setTextSize(18); view.addView(status);
        Button scan = new Button(this); scan.setText("Сканировать QR с компьютера");
        scan.setOnClickListener(v -> startActivityForResult(new Intent(this, Scan.class), 1)); view.addView(scan);
        send = new Button(this); send.setText(R.string.tile_send); send.setOnClickListener(v -> transmit()); view.addView(send);
        clipboardSend = new Button(this); clipboardSend.setText("Отправить буфер телефона");
        clipboardSend.setOnClickListener(v -> sendClipboard()); view.addView(clipboardSend);
        pull = new Button(this); pull.setText(R.string.tile_pull);
        pull.setOnClickListener(v -> transfer(null)); view.addView(pull);
        TextView hint = new TextView(this); hint.setPadding(0, pad, 0, 0);
        hint.setText(R.string.tiles_hint); view.addView(hint);
        if (Build.VERSION.SDK_INT >= 33) {
            Button addTile = new Button(this); addTile.setText("Добавить плитку");
            addTile.setOnClickListener(v -> chooseTile()); view.addView(addTile);
        }
        ScrollView scroll = new ScrollView(this); scroll.addView(view); setContentView(scroll);
        Intent i = getIntent();
        if (Intent.ACTION_SEND.equals(i.getAction()) && i.getType() != null && i.getType().startsWith("text/")) {
            CharSequence text = i.getCharSequenceExtra(Intent.EXTRA_TEXT);
            if (text != null) shared = text.toString();
            else shared = Transfer.textFrom(i.getClipData());
        }
        send.setVisibility(shared == null ? View.GONE : View.VISIBLE);
        boolean paired = Pair.file(this).getBaseFile().exists();
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
            try { Pair.save(this,data.getStringExtra("qr")); status.setText("Компьютер привязан.");
                  if (shared != null) transmit(); }
            catch (Exception e) { status.setText("QR не подходит или ключ не удалось сохранить."); }
        }
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
        sending = true; send.setEnabled(false); pull.setEnabled(false); clipboardSend.setEnabled(false); status.setText(Transfer.progress(value));
        Transfer.run(this, value, result -> {
            sending = false; send.setEnabled(true); pull.setEnabled(true); clipboardSend.setEnabled(true);
            status.setText(result.message);
        });
    }
}
