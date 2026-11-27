package ru.buffer;

import android.content.Context;
import android.os.Build;
import android.os.SystemClock;
import android.provider.Settings;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import javax.crypto.spec.SecretKeySpec;

final class PhoneIdentity {
    private static final byte[] AAD = "buffer/phone-name/v1".getBytes(StandardCharsets.UTF_8);
    private static final int MAX_CODE_POINTS = 64;
    private static Pair lastPair;
    private static String lastName;
    private static long attemptedAt, cooldown;

    static String normalize(String value) {
        if (value == null) return "";
        StringBuilder result = new StringBuilder();
        int count = 0;
        boolean space = false;
        for (int i = 0; i < value.length() && count < MAX_CODE_POINTS;) {
            int cp = value.codePointAt(i);
            i += Character.charCount(cp);
            if (Character.isWhitespace(cp) || Character.isSpaceChar(cp)) {
                space = count > 0;
                continue;
            }
            int type = Character.getType(cp);
            if (Character.isISOControl(cp) || type == Character.FORMAT || type == Character.SURROGATE) continue;
            if (space) {
                if (count == MAX_CODE_POINTS - 1) break;
                result.append(' ');
                count++;
                space = false;
            }
            result.appendCodePoint(cp);
            count++;
        }
        return result.toString();
    }

    private static String name(Context context) {
        String name = "";
        try {
            name = normalize(Settings.Global.getString(context.getContentResolver(), Settings.Global.DEVICE_NAME));
        } catch (SecurityException ignored) {
            // Some OEMs restrict access to system settings; the model is public.
        }
        if (name.isEmpty() || Build.UNKNOWN.equalsIgnoreCase(name)) name = normalize(Build.MODEL);
        return name.isEmpty() || Build.UNKNOWN.equalsIgnoreCase(name) ? "Android" : name;
    }

    // Only called on Transfer's network queue. Announcements are never a requirement for text delivery.
    static void publish(Context context, Pair pair) {
        try {
            String name = name(context);
            long now = SystemClock.elapsedRealtime();
            if (lastPair != null && lastName.equals(name) && lastPair.url.equals(pair.url)
                && lastPair.token.equals(pair.token) && Arrays.equals(lastPair.key, pair.key)
                && now - attemptedAt < cooldown) return;
            lastPair = pair;
            lastName = name;
            attemptedAt = now;
            cooldown = 30_000;

            SecretKeySpec key = new SecretKeySpec(pair.key, "AES");
            byte[] body = Pair.seal(key, name.getBytes(StandardCharsets.UTF_8), AAD);
            HttpURLConnection c = Transfer.preparePost(pair, "/clip", body.length, 1500, 2000);
            try {
                try (OutputStream out = c.getOutputStream()) { out.write(body); }
                int status = c.getResponseCode();
                if (status == 204 || status == 422) cooldown = 120_000;
                else if (status == 409 || status == 503) cooldown = 3_000;
            } finally {
                c.disconnect();
            }
        } catch (Exception ignored) {
            // Offline or unsupported desktops will get another chance on later user activity.
        }
    }
}
