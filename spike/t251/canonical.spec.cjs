const { expect, test } = require('@playwright/test');

for (const width of [1280, 390]) for (const colorScheme of ['light', 'dark']) {
  test(`documentation ${width} ${colorScheme}`, async ({ page, context, baseURL }) => {
    const errors = [];
    const external = [];
    const origin = new URL(baseURL).origin;
    page.on('pageerror', error => errors.push(error.message));
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
    page.on('request', request => {
      if (new URL(request.url()).origin !== origin) external.push(request.url());
    });

    // This is the actual handler/auth/session chain, without API interception.
    const request = context.request;
    expect((await request.get('/documentation/docs/README.md')).status()).toBe(401);
    const login = await request.post('/api/auth/login', {
      data: { email: 'docs@example.com', password: 'documentation-password' },
    });
    expect(login.status()).toBe(200);
    await page.setViewportSize({ width, height: width === 390 ? 844 : 800 });
    await page.emulateMedia({ colorScheme, reducedMotion: 'reduce' });

    const capture = async name => {
      await page.evaluate(() => document.fonts.ready);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await expect(page).toHaveScreenshot(`documentation-${name}--${width}--${colorScheme}.png`);
    };

    await page.goto('/documentation/docs/README.md', { waitUntil: 'networkidle' });
    await expect(page).toHaveTitle('docs/README.md · phebs');
    await capture('index');

    await page.getByRole('link', { name: 'MANUAL.md', exact: true }).first().click();
    await page.getByRole('link', { name: 'Getting started', exact: true }).click();
    await expect(page).toHaveURL(/\/documentation\/docs\/guides\/GETTING_STARTED\.md$/);
    await expect(page.getByRole('heading', { name: 'Getting started', exact: true })).toBeVisible();
    await capture('guide');

    await page.goto('/documentation/spike/t304/README.md', { waitUntil: 'networkidle' });
    await expect(page.getByRole('heading', { name: 'Repository-only reference', exact: true })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Open in the public repository', exact: true }))
      .toHaveAttribute('href', 'https://github.com/bmeddeb/phebs/blob/main/spike/t304/README.md');
    await capture('repository-only');

    expect(errors).toEqual([]);
    expect(external).toEqual([]);
  });
}
