#!/usr/bin/env node

import { access, readdir, readFile, unlink, writeFile } from "node:fs/promises";
import { join } from "node:path";

const frontendRoot = process.argv[2];
if (!frontendRoot) {
  throw new Error("usage: patch-komari-web.mjs <komari-web-directory>");
}

function replaceOnce(source, needle, replacement, label) {
  const index = source.indexOf(needle);
  if (index < 0) throw new Error(`frontend patch target not found: ${label}`);
  return source.slice(0, index) + replacement + source.slice(index + needle.length);
}

function removeBetween(source, startNeedle, endNeedle, label) {
  const start = source.indexOf(startNeedle);
  if (start < 0) throw new Error(`frontend patch start not found: ${label}`);
  const end = source.indexOf(endNeedle, start);
  if (end < 0) throw new Error(`frontend patch end not found: ${label}`);
  return source.slice(0, start) + source.slice(end);
}

function removeFrom(source, startNeedle, label) {
  const start = source.indexOf(startNeedle);
  if (start < 0) throw new Error(`frontend patch start not found: ${label}`);
  return source.slice(0, start);
}

function removeRegexOnce(source, pattern, label) {
  const match = source.match(pattern);
  if (!match || match.index === undefined) {
    throw new Error(`frontend patch regex target not found: ${label}`);
  }
  return source.slice(0, match.index) + source.slice(match.index + match[0].length);
}

async function patchInstallPage() {
  const path = join(frontendRoot, "src/pages/install.tsx");
  let source = await readFile(path, "utf8");

  source = replaceOnce(
    source,
    'import { isSQLiteDSN } from "@/utils/metric";\n',
    "",
    "install SQLite helper import",
  );
  source = replaceOnce(
    source,
    '  const [metricDSN, setMetricDSN] = useState("./data/metrics.db");\n',
    "",
    "install metric DSN state",
  );
  source = replaceOnce(
    source,
    '    if (step === 3 && !metricDSN.trim())\n      return setError(t("install.dsn_required"));\n',
    "",
    "install metric DSN validation",
  );
  source = replaceOnce(
    source,
    '    setStep((current) => Math.min(current + 1, 4));\n',
    '    setStep((current) => Math.min(current + 1, 3));\n',
    "install step count",
  );
  source = replaceOnce(
    source,
    '    if (step !== 4) return next();\n',
    '    if (step !== 3) return next();\n',
    "install submit step",
  );
  source = replaceOnce(
    source,
    '          description,\n          metric_dsn: metricDSN,\n',
    '          description,\n',
    "install metric DSN request field",
  );
  source = replaceOnce(
    source,
    '  const titles = ["welcome", "administrator", "site", "database", "confirm"];\n',
    '  const titles = ["welcome", "administrator", "site", "confirm"];\n',
    "install titles",
  );

  source = removeBetween(
    source,
    "            {step === 3 && (",
    "            {step === 4 && (",
    "install database step",
  );
  source = source.replaceAll("step < 4", "step < 3");
  source = source.replaceAll("step === 4", "step === 3");
  source = source.replace(
    /\n\s*<Text size="2" className="break-all">\s*<strong>\{t\("install\.summary_dsn"\)\}<\/strong>\s*\{metricDSN\}\s*<\/Text>/,
    "",
  );

  if (source.includes("metricDSN") || source.includes("metric_dsn")) {
    throw new Error("frontend install page still contains an independent metric DSN");
  }
  await writeFile(path, source);
}

async function patchMetricsSettingsPage() {
  const path = join(frontendRoot, "src/pages/admin/settings/metrics.tsx");
  let source = await readFile(path, "utf8");
  source = source.replace(
    /\nconst DSN_PLACEHOLDER =\n  "[^"]*";\n/,
    "\n",
  );
  source = removeBetween(
    source,
    '      <SettingCardShortTextInput\n        title={t("settings.metrics.dsn_title")}',
    '      <SettingCardLabel>\n        {t("settings.metrics.advanced_title")}',
    "metrics settings DSN card",
  );
  source = replaceOnce(
    source,
    '      <SettingCardLabel>\n        {t("settings.metrics.migration_title")}\n      </SettingCardLabel>\n      <MigrationCard />\n',
    "",
    "metrics settings migration card",
  );
  source = removeRegexOnce(
    source,
    /\n      <SettingCardShortTextInput\n        title=\{t\("settings\.metrics\.max_open_conns_title"\)\}[\s\S]*?\n      \/>/,
    "metrics max-open-conns card",
  );
  source = removeRegexOnce(
    source,
    /\n      <SettingCardShortTextInput\n        title=\{t\("settings\.metrics\.max_idle_conns_title"\)\}[\s\S]*?\n      \/>/,
    "metrics max-idle-conns card",
  );
  source = removeBetween(
    source,
    "\n// store-to-store 迁移状态。",
    "\ninterface MetricDefinition",
    "metrics migration types",
  );
  source = removeFrom(source, "\nfunction StatusBadge", "metrics migration implementation");
  for (const importLine of ["  Database,\n", "  Progress,\n", "  X,\n"]) {
    source = source.replace(importLine, "");
  }
  if (
    source.includes("metric_db_dsn") ||
    source.includes("source_dsn") ||
    source.includes("MigrationCard") ||
    source.includes('settings.metrics.dsn_title')
  ) {
    throw new Error("metrics settings page still exposes an independent metric DSN");
  }
  await writeFile(path, source);
}

async function patchDatabaseMigrationPage() {
  const path = join(frontendRoot, "src/pages/database_migration.tsx");
  let source = await readFile(path, "utf8");

  for (const importLine of [
    "  SegmentedControl,\n",
    "  TextField,\n",
    "  HardDrive,\n",
    "  Server,\n",
  ]) {
    source = source.replace(importLine, "");
  }
  source = removeBetween(
    source,
    'type Driver = "sqlite" | "mysql" | "postgresql";\n\n',
    "type Summary =",
    "database migration driver type",
  );
  source = source.replace("  target_driver?: Driver;\n", "  target_driver?: string;\n");
  source = removeBetween(
    source,
    "\nconst examples: Record<Driver, string> = {",
    "\n\nasync function request",
    "database migration DSN examples",
  );
  source = removeRegexOnce(
    source,
    /\n  const \[driver, setDriver\] = useState<Driver>\("sqlite"\);\n  const \[dsn, setDSN\] = useState\(examples\.sqlite\);/,
    "database migration DSN state",
  );
  source = source.replace("  const [confirmSQLite, setConfirmSQLite] = useState(false);\n", "");
  source = removeRegexOnce(
    source,
    /\n  const sqliteRisk =[\s\S]*?(?=\n  const largeRisk =)/,
    "database migration SQLite risk",
  );
  source = removeBetween(
    source,
    "\n  const handleDriver = (value: string) => {",
    "\n  const start = async () => {",
    "database migration DSN handlers",
  );
  const legacyRequest = [
    "        body: legacy",
    "          ? JSON.stringify({",
    "              driver,",
    "              dsn,",
    "              confirm_sqlite_risk: confirmSQLite,",
    "              confirm_large_dataset: confirmLarge,",
    "            })",
    "          : undefined,",
  ].join("\n");
  source = replaceOnce(
    source,
    legacyRequest,
    [
      "        body: legacy",
      "          ? JSON.stringify({ confirm_large_dataset: confirmLarge })",
      "          : undefined,",
    ].join("\n"),
    "database migration start request",
  );
  for (const propLine of [
    "                driver={driver}\n",
    "                dsn={dsn}\n",
    "                active={active}\n",
    "                onDriverChange={handleDriver}\n",
    "                onDSNChange={setDSN}\n",
  ]) {
    source = source.replace(propLine, "");
  }
  source = removeBetween(
    source,
    "              {sqliteRisk && (",
    "              {largeRisk && (",
    "database migration SQLite confirmation",
  );
  source = source.replace(
    "                busy ||\n                (sqliteRisk && !confirmSQLite) ||\n                (largeRisk && !confirmLarge)",
    "                busy ||\n                (largeRisk && !confirmLarge)",
  );
  source = removeBetween(
    source,
    "\ntype LegacySetupProps =",
    "\nfunction Completion",
    "database migration legacy target component",
  );
  const legacySetup = [
    "",
    "type LegacySetupProps = {",
    "  summary: Summary;",
    "  locale: string;",
    "};",
    "",
    "function LegacySetup({ summary, locale }: LegacySetupProps) {",
    "  const { t } = useTranslation();",
    "  const number = (value: number) => new Intl.NumberFormat(locale).format(value);",
    "  return (",
    "    <Flex direction=\"column\" gap=\"5\">",
    "      <Flex direction=\"column\" gap=\"3\">",
    "        <SettingCardLabel>{t(`${LEGACY_I18N}.overview`)}</SettingCardLabel>",
    "        <div className=\"km-database-migration-stats grid gap-3 sm:grid-cols-2\">",
    "          <SettingCard",
    "            className=\"km-database-migration-card\"",
    "            title={t(`${LEGACY_I18N}.load_rows`)}",
    "          >",
    "            <Flex direction=\"column\" gap=\"1\" className=\"mt-2 w-full\">",
    "              <Text size=\"7\" weight=\"bold\" className=\"tabular-nums\">",
    "                {number(summary.load_rows)}{\" \"}",
    "                <Text size=\"2\" color=\"gray\">",
    "                  {t(`${LEGACY_I18N}.rows`)}",
    "                </Text>",
    "              </Text>",
    "              <Text size=\"1\" color=\"gray\">",
    "                {t(`${LEGACY_I18N}.gpu_rows`, { count: summary.gpu_rows })}",
    "              </Text>",
    "            </Flex>",
    "          </SettingCard>",
    "          <SettingCard",
    "            className=\"km-database-migration-card\"",
    "            title={t(`${LEGACY_I18N}.latency_rows`)}",
    "          >",
    "            <Text size=\"7\" weight=\"bold\" className=\"mt-2 tabular-nums\">",
    "              {number(summary.latency_rows)}{\" \"}",
    "              <Text size=\"2\" color=\"gray\">",
    "                {t(`${LEGACY_I18N}.rows`)}",
    "              </Text>",
    "            </Text>",
    "          </SettingCard>",
    "        </div>",
    "        <Text size=\"2\" color=\"gray\">",
    "          {t(`${LEGACY_I18N}.overview_detail`, {",
    "            points: number(summary.estimated_points),",
    "            days: summary.retention_days,",
    "            servers: summary.server_count,",
    "          })}",
    "        </Text>",
    "      </Flex>",
    "    </Flex>",
    "  );",
    "}",
    "",
  ].join("\n");
  source = replaceOnce(
    source,
    "\nfunction Completion",
    `${legacySetup}function Completion`,
    "database migration fixed target component",
  );
  if (
    source.includes("metric_dsn") ||
    source.includes("source_dsn") ||
    source.includes("setDSN") ||
    source.includes("SegmentedControl") ||
    source.includes("SQLite") ||
    source.includes("MySQL")
  ) {
    throw new Error("database migration page still exposes a selectable metric backend");
  }

  await writeFile(path, source);
}

async function patchRemovedRecoveryPage() {
  const routesPath = join(frontendRoot, "src/routes.ts");
  let routes = await readFile(routesPath, "utf8");
  routes = removeBetween(
    routes,
    '  {\n    path: "/database-recovery",',
    '  {\n    path: "/admin/login",',
    "database recovery route",
  );
  await writeFile(routesPath, routes);

  const mainPath = join(frontendRoot, "src/main.tsx");
  let main = await readFile(mainPath, "utf8");
  main = replaceOnce(
    main,
    '    "/database-recovery",\n',
    "",
    "database recovery restricted path",
  );
  await writeFile(mainPath, main);

  const recoveryPath = join(frontendRoot, "src/pages/database_recovery.tsx");
  try {
    await access(recoveryPath);
    await unlink(recoveryPath);
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
}

async function patchFrontendBuildCompatibility() {
  const path = join(frontendRoot, "src/components/admin/AdminPanelBar.tsx");
  let source = await readFile(path, "utf8");
  source = replaceOnce(
    source,
    "  };\n\n  // 内容区域动画变体",
    "  } as const;\n\n  // 内容区域动画变体",
    "sidebar variants literal type",
  );
  await writeFile(path, source);
}

async function patchInstallTranslations() {
  const localeDir = join(frontendRoot, "src/i18n/locales");
  const files = (await readdir(localeDir)).filter((name) => name.endsWith(".json"));
  for (const name of files) {
    const path = join(localeDir, name);
    const data = JSON.parse(await readFile(path, "utf8"));
    if (data.install) {
      delete data.install.database;
      delete data.install.database_title;
      delete data.install.detected_database;
      delete data.install.dsn_required;
      delete data.install.sqlite_warning;
      delete data.install.summary_database;
      delete data.install.summary_dsn;
      if (data.install.steps) delete data.install.steps.database;
      data.install.welcome_description =
        name === "zh_CN.json"
          ? "配置管理员账号和站点信息。"
          : name === "zh_TW.json"
            ? "設定管理員帳號與站點資訊。"
            : data.install.welcome_description;
    }
    delete data.database_recovery;
    await writeFile(path, `${JSON.stringify(data, null, 2)}\n`);
  }
}

await patchInstallPage();
await patchMetricsSettingsPage();
await patchDatabaseMigrationPage();
await patchRemovedRecoveryPage();
await patchFrontendBuildCompatibility();
await patchInstallTranslations();
