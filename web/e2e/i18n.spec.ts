import { expect, test } from '@playwright/test';
import { en } from '../src/lib/messages';
import { L } from './i18n';

test('Hebrew right-to-left by default; the header toggle switches to English and is remembered', async ({ page }) => {
  await page.goto('/#/open-finance');
  const html = page.locator('html');
  await expect(html).toHaveAttribute('lang', 'he');
  await expect(html).toHaveAttribute('dir', 'rtl');
  await expect(page.getByRole('heading', { level: 1, name: L('title.openFinance') })).toBeVisible();

  const langs = page.getByRole('group', { name: L('lang.label') });
  const hebrew = langs.getByRole('button', { name: 'עברית' });
  const english = langs.getByRole('button', { name: 'English' });
  await expect(hebrew).toHaveAttribute('aria-pressed', 'true');
  await expect(english).toHaveAttribute('aria-pressed', 'false');
  for (const b of [hebrew, english]) {
    const box = await b.boundingBox();
    expect(box!.height).toBeGreaterThanOrEqual(44);
    expect(box!.width).toBeGreaterThanOrEqual(44);
  }

  await english.click();
  await expect(html).toHaveAttribute('dir', 'ltr');
  await expect(html).toHaveAttribute('lang', 'en');
  await expect(page.getByRole('heading', { level: 1, name: en['title.openFinance'] })).toBeVisible();
  // The group's own name is translated too.
  const englishNow = page.getByRole('group', { name: en['lang.label'] }).getByRole('button', { name: 'English' });
  await expect(englishNow).toHaveAttribute('aria-pressed', 'true');

  await page.reload();
  await expect(page.getByRole('heading', { level: 1, name: en['title.openFinance'] })).toBeVisible();
  await expect(html).toHaveAttribute('dir', 'ltr');

  // Leave the default for the specs that follow (same origin, same storage).
  await page.getByRole('group', { name: en['lang.label'] }).getByRole('button', { name: 'עברית' }).click();
  await expect(html).toHaveAttribute('dir', 'rtl');
  await expect(page.getByRole('heading', { level: 1, name: L('title.openFinance') })).toBeVisible();
});
