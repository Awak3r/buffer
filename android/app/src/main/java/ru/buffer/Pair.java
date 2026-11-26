package ru.buffer;

import android.content.Context;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.AtomicFile;
import android.util.Base64;
import org.json.JSONObject;
import java.io.*;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.KeyStore;
import java.util.Arrays;
import javax.crypto.*;
import javax.crypto.spec.*;

final class Pair {
    final String url, token;
    final byte[] key;
    Pair(String json) throws Exception {
        JSONObject p = new JSONObject(json);
        url = p.getString("url");
        URI u = new URI(url);
        token = p.getString("token");
        key = Base64.decode(p.getString("key"), Base64.NO_WRAP);
        if (!"https".equals(u.getScheme()) || u.getHost() == null || u.getUserInfo() != null ||
            !u.getRawPath().isEmpty() || u.getQuery() != null || u.getFragment() != null ||
            !token.matches("[!-~]{32,256}") || key.length != 32) throw new IOException("Invalid pairing");
    }
    static AtomicFile file(Context c) { return new AtomicFile(new File(c.getNoBackupFilesDir(), "pair")); }
    private static SecretKey localKey() throws Exception {
        KeyStore store = KeyStore.getInstance("AndroidKeyStore");
        store.load(null);
        String alias = "buffer.pair";
        if (store.containsAlias(alias)) return (SecretKey)store.getKey(alias, null);
        KeyGenerator gen = KeyGenerator.getInstance("AES", "AndroidKeyStore");
        gen.init(new KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes("GCM").setEncryptionPaddings("NoPadding").build());
        return gen.generateKey();
    }
    static byte[] seal(SecretKey key, byte[] plain) throws Exception {
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.ENCRYPT_MODE, key);
        byte[] iv = cipher.getIV();
        if (iv.length != 12) throw new IOException("Invalid nonce size");
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        out.write(iv); out.write(cipher.doFinal(plain));
        return out.toByteArray();
    }
    static void save(Context c, String json) throws Exception {
        new Pair(json);
        byte[] bytes = seal(localKey(), json.getBytes(StandardCharsets.UTF_8));
        AtomicFile f = file(c);
        FileOutputStream out = f.startWrite();
        try { out.write(bytes); f.finishWrite(out); }
        catch (IOException e) { f.failWrite(out); throw e; }
    }
    static Pair load(Context c) throws Exception {
        byte[] b = file(c).readFully();
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.DECRYPT_MODE, localKey(), new GCMParameterSpec(128, b, 0, 12));
        return new Pair(new String(cipher.doFinal(Arrays.copyOfRange(b,12,b.length)), StandardCharsets.UTF_8));
    }
}
