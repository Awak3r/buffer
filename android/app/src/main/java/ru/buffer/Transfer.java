package ru.buffer;

import android.app.Activity;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.Executor;
import java.util.concurrent.Executors;
import java.util.function.Consumer;
import javax.crypto.Cipher;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;

final class Transfer {
    private static final int MAX_TEXT_BYTES = 65536;
    // The relay accepts one request at a time; phone labels share that slot with text.
    private static final Executor NETWORK = Executors.newSingleThreadExecutor();
    static final String INVALID_TEXT = "Нужен текст до 64 КиБ без нулевых символов.";

    static final class Result {
        final boolean success;
        final String message;
        final String pulledText;

        private Result(boolean success, String message, String pulledText) {
            this.success = success;
            this.message = message;
            this.pulledText = pulledText;
        }
    }

    private static Result error(String message) { return new Result(false, message, null); }

    static String textFrom(ClipData clip) {
        if (clip == null) return null;
        StringBuilder text = new StringBuilder();
        for (int i = 0; i < clip.getItemCount(); i++) {
            ClipData.Item item = clip.getItemAt(i);
            CharSequence part = item.getText();
            if (part == null && item.getUri() != null) {
                String scheme = item.getUri().getScheme();
                if ("https".equals(scheme) || "http".equals(scheme)) part = item.getUri().toString();
            }
            if (part != null) {
                if (text.length() > 0) text.append('\n');
                text.append(part);
            }
        }
        return text.length() == 0 ? null : text.toString();
    }

    static String readClipboard(Activity activity) {
        return textFrom(activity.getSystemService(ClipboardManager.class).getPrimaryClip());
    }

    static boolean valid(String value) {
        return value == null || (!value.isEmpty() && value.indexOf(0) < 0
            && value.getBytes(StandardCharsets.UTF_8).length <= MAX_TEXT_BYTES);
    }

    static String progress(String value) {
        return value == null ? "Получаю с компьютера…" : "Передаю на компьютер…";
    }

    static void run(Activity activity, String value, Consumer<Result> done) {
        byte[] plain = value == null ? null : value.getBytes(StandardCharsets.UTF_8);
        NETWORK.execute(() -> {
            Result response;
            try {
                Pair pair = Pair.load(activity);
                response = post(pair, plain);
                if (response.success) announce(activity, pair);
            }
            catch (Exception e) {
                response = error(plain == null ? "Не удалось получить текст. Проверь адрес сервера и сеть."
                    : "Подтверждения нет. Проверь буфер ПК, адрес сервера и сеть.");
            }
            Result result = response;
            activity.runOnUiThread(() -> {
                if (activity.isFinishing() || activity.isDestroyed()) return;
                if (result.pulledText != null) {
                    if (!activity.hasWindowFocus()) {
                        done.accept(error("Повтори получение, не закрывая окно Буфера."));
                        return;
                    }
                    activity.getSystemService(ClipboardManager.class)
                        .setPrimaryClip(ClipData.newPlainText("Буфер ПК", result.pulledText));
                }
                done.accept(result);
            });
        });
    }

    static void announce(Context context, Pair pair) {
        Context app = context.getApplicationContext();
        NETWORK.execute(() -> PhoneIdentity.publish(app, pair));
    }

    static HttpURLConnection preparePost(Pair pair, String path, int size, int connectTimeout, int readTimeout)
        throws IOException {
        HttpURLConnection c = (HttpURLConnection)new URL(pair.url + path).openConnection();
        c.setInstanceFollowRedirects(false);
        c.setConnectTimeout(connectTimeout);
        c.setReadTimeout(readTimeout);
        c.setRequestMethod("POST");
        c.setDoOutput(true);
        c.setFixedLengthStreamingMode(size);
        c.setRequestProperty("Authorization", "Bearer " + pair.token);
        c.setRequestProperty("Content-Type", "application/octet-stream");
        return c;
    }

    private static Result post(Pair p, byte[] plain) throws Exception {
        boolean pulling = plain == null;
        SecretKeySpec key = new SecretKeySpec(p.key, "AES");
        byte[] body = pulling ? Pair.seal(key, new byte[0], "buffer/pull/v1".getBytes(StandardCharsets.UTF_8)) : Pair.seal(key, plain);
        HttpURLConnection c = preparePost(p, pulling ? "/pull" : "/clip", body.length, 5000, 12000);
        try {
            try (OutputStream out = c.getOutputStream()) { out.write(body); }
            switch (c.getResponseCode()) {
                case 200:
                    if (!pulling) throw new IOException("Unexpected response");
                    ByteArrayOutputStream bytes = new ByteArrayOutputStream();
                    try (InputStream in = c.getInputStream()) {
                        byte[] chunk = new byte[4096]; int n;
                        while ((n = in.read(chunk)) != -1) {
                            if (bytes.size() + n > MAX_TEXT_BYTES + 28) throw new IOException("Size limit");
                            bytes.write(chunk, 0, n);
                        }
                    }
                    byte[] result = bytes.toByteArray();
                    if (result.length < 29) throw new IOException("Invalid response");
                    Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
                    cipher.init(Cipher.DECRYPT_MODE, key, new GCMParameterSpec(128, result, 0, 12));
                    cipher.updateAAD(body);
                    String text = new String(cipher.doFinal(result, 12, result.length - 12), StandardCharsets.UTF_8);
                    return new Result(true, "Скопировано в буфер телефона", text);
                case 404: return error("Обнови ретранслятор на VPS для получения с ПК.");
                case 204:
                    if (pulling) throw new IOException("Missing clipboard");
                    return new Result(true, "Готово к вставке на ПК", null);
                case 503: return error("Компьютер офлайн");
                case 401: return error("Сервер отклонил токен пары");
                case 409: return error("ПК ещё обрабатывает предыдущий запрос");
                case 422: return error(pulling ? "Обнови клиент ПК и проверь, что в буфере есть текст до 64 КиБ."
                    : "ПК не смог записать текст. Проверь привязку и буфер.");
                default: return error(pulling ? "Не удалось получить буфер ПК." : "Доставка не подтверждена. Проверь буфер ПК.");
            }
        } finally { c.disconnect(); }
    }
}
