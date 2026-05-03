import { expect, test } from '@playwright/test';

test('loads the CloudConv app shell', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('body')).toContainText('CloudConv');
});

test('admin panel renders empty tables', async ({ page, request }) => {
  const email = 'admin@example.com';
  const password = 'password123';
  const config = await (await request.get('/api/config')).json();
  if (config.setupNeeded) {
    await request.post('/api/setup', {
      data: { email, password, setupToken: 'playwright-token' }
    });
  }

  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Login' }).click();
  await page.waitForURL('**/admin');

  await expect(page.getByRole('heading', { name: 'Admin' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Users' })).toBeVisible();
  await page.getByRole('button', { name: 'Jobs' }).click();
  await expect(page.getByRole('heading', { name: 'Jobs' })).toBeVisible();
  await expect(page.locator('body')).toContainText('No records found.');
});
