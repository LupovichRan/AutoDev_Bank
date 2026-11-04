package bank.autodev.shell;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.SharedPreferences;
import android.graphics.Bitmap;
import android.net.Uri;
import android.os.Bundle;
import android.text.InputType;
import android.view.Menu;
import android.view.MenuItem;
import android.view.View;
import android.webkit.CookieManager;
import android.webkit.GeolocationPermissions;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;

/**
 * AutoDev Bank test shell: one WebView that loads the PWA from the bank server.
 *
 * The server URL is asked for on first start and kept in SharedPreferences.
 * The web app is served by the Go binary itself (same origin as the API), so
 * cookies and CSP behave exactly as in a browser. Plain http is allowed only
 * because this is a test build (see res/xml/network_security_config.xml).
 */
public class MainActivity extends Activity {
    private static final String PREFS = "autodev";
    private static final String KEY_SERVER = "server_url";

    private WebView web;
    private SharedPreferences prefs;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        prefs = getSharedPreferences(PREFS, MODE_PRIVATE);

        web = new WebView(this);
        setContentView(web);
        configure(web);

        if (savedInstanceState != null) {
            web.restoreState(savedInstanceState);
            return;
        }
        String server = prefs.getString(KEY_SERVER, null);
        if (server == null) {
            askForServer(null);
        } else {
            web.loadUrl(server);
        }
    }

    private void configure(WebView w) {
        WebSettings s = w.getSettings();
        s.setJavaScriptEnabled(true);          // the PWA is a JavaScript app
        s.setDomStorageEnabled(true);          // localStorage for language and cached balances
        s.setDatabaseEnabled(true);
        s.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        s.setAllowFileAccess(false);
        s.setAllowContentAccess(false);
        s.setGeolocationEnabled(true);         // "Nearby ATMs" asks for location
        s.setSupportZoom(false);
        s.setUserAgentString(s.getUserAgentString() + " AutoDevBankShell/0.1");
        CookieManager.getInstance().setAcceptCookie(true);
        CookieManager.getInstance().setAcceptThirdPartyCookies(w, false);
        w.setLayerType(View.LAYER_TYPE_HARDWARE, null);

        w.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                // Stay inside the bank server; open anything else (source links, terms) in the browser.
                Uri url = request.getUrl();
                String server = prefs.getString(KEY_SERVER, "");
                if (server.isEmpty() || url.toString().startsWith(server)) {
                    return false;
                }
                try {
                    startActivity(new android.content.Intent(android.content.Intent.ACTION_VIEW, url));
                } catch (RuntimeException ignored) {
                    return false;
                }
                return true;
            }

            @Override
            public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                if (request.isForMainFrame()) {
                    showError(String.valueOf(error.getDescription()));
                }
            }
        });

        w.setWebChromeClient(new WebChromeClient() {
            @Override
            public void onGeolocationPermissionsShowPrompt(String origin, GeolocationPermissions.Callback callback) {
                callback.invoke(origin, true, false);
            }
        });
    }

    private void askForServer(String message) {
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        int pad = (int) (20 * getResources().getDisplayMetrics().density);
        box.setPadding(pad, pad / 2, pad, 0);

        TextView help = new TextView(this);
        help.setText(message != null ? message : getString(R.string.server_help));
        box.addView(help);

        EditText input = new EditText(this);
        input.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        input.setHint(R.string.server_hint);
        input.setText(prefs.getString(KEY_SERVER, ""));
        box.addView(input);

        new AlertDialog.Builder(this)
                .setTitle(R.string.server_title)
                .setView(box)
                .setCancelable(prefs.contains(KEY_SERVER))
                .setPositiveButton(R.string.connect, (d, which) -> {
                    String url = normalise(input.getText().toString());
                    if (url == null) {
                        askForServer(getString(R.string.server_help));
                        return;
                    }
                    prefs.edit().putString(KEY_SERVER, url).apply();
                    web.loadUrl(url);
                })
                .setNegativeButton(R.string.cancel, null)
                .show();
    }

    /** Accepts "host:port", "http://host:port" or "https://host"; returns an origin with no trailing slash. */
    static String normalise(String raw) {
        String s = raw == null ? "" : raw.trim();
        if (s.isEmpty()) return null;
        if (!s.startsWith("http://") && !s.startsWith("https://")) s = "http://" + s;
        while (s.endsWith("/")) s = s.substring(0, s.length() - 1);
        Uri u = Uri.parse(s);
        if (u.getHost() == null || u.getHost().isEmpty()) return null;
        return s;
    }

    private void showError(String description) {
        new AlertDialog.Builder(this)
                .setTitle(R.string.error_title)
                .setMessage(getString(R.string.error_body, description))
                .setPositiveButton(R.string.retry, (d, w) -> web.reload())
                .setNegativeButton(R.string.menu_server, (d, w) -> askForServer(null))
                .show();
    }

    @Override
    public boolean onCreateOptionsMenu(Menu menu) {
        getMenuInflater().inflate(R.menu.main, menu);
        return true;
    }

    @Override
    public boolean onOptionsItemSelected(MenuItem item) {
        int id = item.getItemId();
        if (id == R.id.action_reload) {
            web.reload();
            return true;
        }
        if (id == R.id.action_server) {
            askForServer(null);
            return true;
        }
        return super.onOptionsItemSelected(item);
    }

    @Override
    public void onBackPressed() {
        if (web.canGoBack()) {
            web.goBack();
        } else {
            super.onBackPressed();
        }
    }

    @Override
    protected void onSaveInstanceState(Bundle outState) {
        super.onSaveInstanceState(outState);
        web.saveState(outState);
    }
}
