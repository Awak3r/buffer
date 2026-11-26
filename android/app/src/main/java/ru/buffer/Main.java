package ru.buffer;

import android.app.Activity;
import android.content.Intent;
import android.os.Bundle;
import android.widget.*;
import java.net.*;
import java.io.*;
import java.nio.charset.StandardCharsets;
import javax.crypto.spec.SecretKeySpec;

public class Main extends Activity {
    private TextView status;
    private Button send;
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
        send = new Button(this); send.setText("Отправить на ПК"); send.setOnClickListener(v -> transmit()); view.addView(send);
        setContentView(view);
        Intent i = getIntent();
        if (Intent.ACTION_SEND.equals(i.getAction()) && "text/plain".equals(i.getType())) {
            CharSequence text = i.getCharSequenceExtra(Intent.EXTRA_TEXT);
            if (text != null) shared = text.toString();
        }
        send.setVisibility(shared == null ? android.view.View.GONE : android.view.View.VISIBLE);
        boolean paired = Pair.file(this).getBaseFile().exists();
        status.setText(paired ? "Выбери текст и нажми «Поделиться» → «На компьютер»." : "Для привязки запусти buffer.exe -pair на ПК.");
        if (shared != null && paired && state == null) transmit();
        else if (state != null) status.setText(state.getString("status", "Готово"));
    }
    @Override public void onSaveInstanceState(Bundle b) {
        super.onSaveInstanceState(b);
        b.putString("status", sending ? "Проверь буфер ПК перед повторной отправкой." : status.getText().toString());
    }
    @Override public void onActivityResult(int req, int result, Intent data) {
        super.onActivityResult(req,result,data);
        if (req == 1 && result == RESULT_OK && data != null) {
            try { Pair.save(this,data.getStringExtra("qr")); status.setText("Компьютер привязан.");
                  if (shared != null) transmit(); }
            catch (Exception e) { status.setText("QR не подходит или ключ не удалось сохранить."); }
        }
    }
    private void transmit() {
        if (sending || shared == null) return;
        byte[] plain = shared.getBytes(StandardCharsets.UTF_8);
        if (plain.length == 0 || plain.length > 65536 || shared.indexOf(0) >= 0) {
            status.setText("Нужен текст до 64 КиБ без нулевых символов."); return;
        }
        sending = true; send.setEnabled(false); status.setText("Передаю на компьютер…");
        new Thread(() -> {
            String message;
            try { message = post(Pair.load(this), plain); }
            catch (Exception e) { message = "Подтверждения нет. Проверь буфер ПК, адрес сервера и сеть."; }
            final String result = message;
            runOnUiThread(() -> { if (!isDestroyed()) { sending = false; send.setEnabled(true); status.setText(result); } });
        }).start();
    }
    private String post(Pair p, byte[] plain) throws Exception {
        byte[] body = Pair.seal(new SecretKeySpec(p.key, "AES"), plain);
        HttpURLConnection c = (HttpURLConnection)new URL(p.url + "/clip").openConnection();
        try {
            c.setInstanceFollowRedirects(false); c.setConnectTimeout(5000); c.setReadTimeout(12000);
            c.setRequestMethod("POST"); c.setDoOutput(true); c.setFixedLengthStreamingMode(body.length);
            c.setRequestProperty("Authorization", "Bearer " + p.token);
            c.setRequestProperty("Content-Type", "application/octet-stream");
            try (OutputStream out = c.getOutputStream()) { out.write(body); }
            switch (c.getResponseCode()) {
                case 204: return "Готово к вставке на ПК";
                case 503: return "Компьютер офлайн";
                case 401: return "Сервер отклонил токен пары";
                case 409: return "ПК ещё принимает предыдущий текст";
                case 422: return "ПК не смог записать текст. Проверь привязку и буфер.";
                default: return "Доставка не подтверждена. Проверь буфер ПК.";
            }
        } finally { c.disconnect(); }
    }
}
