package ru.buffer;

import android.app.Activity;
import android.content.*;
import javax.crypto.Cipher;
import javax.crypto.spec.GCMParameterSpec;
import android.os.Bundle;
import android.widget.*;
import java.net.*;
import java.io.*;
import java.nio.charset.StandardCharsets;
import javax.crypto.spec.SecretKeySpec;

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
        send = new Button(this); send.setText("Отправить на ПК"); send.setOnClickListener(v -> transmit()); view.addView(send);
        clipboardSend = new Button(this); clipboardSend.setText("Отправить буфер телефона");
        clipboardSend.setOnClickListener(v -> sendClipboard()); view.addView(clipboardSend);
        pull = new Button(this); pull.setText("Забрать с ПК");
        pull.setOnClickListener(v -> transfer(null)); view.addView(pull);
        setContentView(view);
        Intent i = getIntent();
        if (Intent.ACTION_SEND.equals(i.getAction()) && i.getType() != null && i.getType().startsWith("text/")) {
            CharSequence text = i.getCharSequenceExtra(Intent.EXTRA_TEXT);
            if (text != null) shared = text.toString();
            else shared = textFrom(i.getClipData());
        }
        send.setVisibility(shared == null ? android.view.View.GONE : android.view.View.VISIBLE);
        boolean paired = Pair.file(this).getBaseFile().exists();
        status.setText(paired ? "Скопируй текст на любом из устройств и выбери направление." : "Для привязки запусти buffer.exe -pair на ПК.");
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
    private String textFrom(ClipData clip) {
        if (clip == null) return null;
        StringBuilder text = new StringBuilder();
        for (int i=0; i<clip.getItemCount(); i++) {
            ClipData.Item item = clip.getItemAt(i);
            CharSequence part = item.getText();
            if (part == null && item.getUri() != null) {
                String scheme = item.getUri().getScheme();
                if ("https".equals(scheme) || "http".equals(scheme)) part = item.getUri().toString();
            }
            if (part != null) {
                if (text.length()>0) text.append('\n');
                text.append(part);
            }
        }
        return text.length()==0 ? null : text.toString();
    }
    private void sendClipboard() {
        String value = textFrom(getSystemService(ClipboardManager.class).getPrimaryClip());
        if (value == null) { status.setText("В буфере телефона нет текста или ссылки."); return; }
        transfer(value);
    }
    private void transmit() { if (shared != null) transfer(shared); }
    private void transfer(String value) {
        if (sending) return;
        if (!Pair.file(this).getBaseFile().exists()) { status.setText("Сначала привяжи компьютер по QR."); return; }
        byte[] plain = value == null ? null : value.getBytes(StandardCharsets.UTF_8);
        if (plain != null && (plain.length == 0 || plain.length > 65536 || value.indexOf(0) >= 0)) {
            status.setText("Нужен текст до 64 КиБ без нулевых символов."); return;
        }
        sending = true; send.setEnabled(false); pull.setEnabled(false); clipboardSend.setEnabled(false); status.setText(value == null ? "Получаю с компьютера…" : "Передаю на компьютер…");
        new Thread(() -> {
            String message;
            try { message = post(Pair.load(this), plain); }
            catch (Exception e) { message = "Подтверждения нет. Проверь буфер ПК, адрес сервера и сеть."; }
            final String result = message;
            runOnUiThread(() -> { if (!isDestroyed()) { sending = false; send.setEnabled(true); pull.setEnabled(true); clipboardSend.setEnabled(true); status.setText(result); } });
        }).start();
    }
    private String post(Pair p, byte[] plain) throws Exception {
        boolean pulling = plain == null;
        SecretKeySpec key = new SecretKeySpec(p.key,"AES");
        byte[] body = pulling ? Pair.seal(key, new byte[0], "buffer/pull/v1".getBytes(StandardCharsets.UTF_8)) : Pair.seal(key,plain);
        HttpURLConnection c = (HttpURLConnection)new URL(p.url + (pulling ? "/pull" : "/clip")).openConnection();
        try {
            c.setInstanceFollowRedirects(false); c.setConnectTimeout(5000); c.setReadTimeout(12000);
            c.setRequestMethod("POST"); c.setDoOutput(true); c.setFixedLengthStreamingMode(body.length);
            c.setRequestProperty("Authorization", "Bearer " + p.token);
            c.setRequestProperty("Content-Type", "application/octet-stream");
            try (OutputStream out = c.getOutputStream()) { out.write(body); }
            switch (c.getResponseCode()) {
                case 200:
                    if (!pulling) throw new IOException("Unexpected response");
                    ByteArrayOutputStream bytes = new ByteArrayOutputStream();
                    try (InputStream in = c.getInputStream()) {
                        byte[] chunk = new byte[4096]; int n;
                        while ((n=in.read(chunk))!=-1) {
                            if (bytes.size()+n > 65536+28) throw new IOException("Size limit");
                            bytes.write(chunk,0,n);
                        }
                    }
                    byte[] result = bytes.toByteArray();
                    if (result.length < 29) throw new IOException("Invalid response");
                    Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
                    cipher.init(Cipher.DECRYPT_MODE,key,new GCMParameterSpec(128,result,0,12));
                    cipher.updateAAD(body);
                    String text = new String(cipher.doFinal(result,12,result.length-12), StandardCharsets.UTF_8);
                    if (isDestroyed() || !hasWindowFocus()) return "Открой приложение и повтори получение.";
                    getSystemService(ClipboardManager.class).setPrimaryClip(ClipData.newPlainText("Буфер ПК",text));
                    return "Скопировано в буфер телефона";
                case 404: return "Обнови ретранслятор на VPS для получения с ПК.";
                case 204: if (pulling) throw new IOException("Missing clipboard"); return "Готово к вставке на ПК";
                case 503: return "Компьютер офлайн";
                case 401: return "Сервер отклонил токен пары";
                case 409: return "ПК ещё принимает предыдущий текст";
                case 422: if (pulling) return "Обнови клиент ПК и проверь, что в буфере есть текст до 64 КиБ."; return "ПК не смог записать текст. Проверь привязку и буфер.";
                default: return "Доставка не подтверждена. Проверь буфер ПК.";
            }
        } finally { c.disconnect(); }
    }
}
