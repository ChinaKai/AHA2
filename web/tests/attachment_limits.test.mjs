import test from "node:test";
import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const dist = resolve(import.meta.dirname, "..", "dist");
const composer = await import(pathToFileURL(resolve(dist, "task_composer.js")));
const mailTask = await import(pathToFileURL(resolve(dist, "mail_task.js")));

const MiB = 1024 * 1024;
const appSource = readFileSync(resolve(dist, "app.js"), "utf8");
const mailSource = readFileSync(resolve(dist, "mail_task.js"), "utf8");

test("Task attachment hint and pre-upload validation use the 8 / 200MiB limit", () => {
  assert.equal(composer.TASK_ATTACHMENT_MAX_COUNT, 8);
  assert.equal(composer.TASK_ATTACHMENT_MAX_BYTES, 200 * MiB);
  assert.equal(composer.taskAttachmentLimitHint(), "每条消息最多 8 个，单文件 ≤200MiB");

  assert.equal(
    composer.taskAttachmentValidationError(7, [{name: "exact.bin", size: 200 * MiB}]),
    "",
    "the boundary size and count are accepted",
  );
  assert.match(
    composer.taskAttachmentValidationError(8, [{name: "ninth.bin", size: 1}]),
    /最多发送 8 个附件/,
  );
  const oversized = composer.taskAttachmentValidationError(0, [
    {name: "ok.bin", size: 1},
    {name: "too-big.bin", size: 200 * MiB + 1},
  ]);
  assert.match(oversized, /超过 200MiB/);
  assert.match(oversized, /too-big\.bin/);
  assert.doesNotMatch(oversized, /ok\.bin/);

  assert.match(appSource, /taskAttachmentLimitHint\(\)/, "the composer title shares the limit hint");
  const validationIndex = appSource.indexOf("taskAttachmentValidationError(drafts.length, files)");
  const uploadIndex = appSource.indexOf("api.uploadAttachment(taskID, file)");
  assert.ok(validationIndex >= 0, "uploads run the shared validator");
  assert.ok(uploadIndex > validationIndex, "oversized selections never reach the upload call");
});

test("mail attachment hint keeps 10 / 25MiB / 20MiB and marks executable rows unavailable", () => {
  assert.equal(mailTask.MAIL_ATTACHMENT_MAX_COUNT, 10);
  assert.equal(mailTask.MAIL_ATTACHMENT_MAX_BYTES, 25 * MiB);
  assert.equal(mailTask.MAIL_DIRECT_ATTACHMENT_THRESHOLD_BYTES, 20 * MiB);
  assert.equal(
    mailTask.MAIL_ATTACHMENT_LIMIT_HINT,
    "最多 10 个、单文件 ≤25MiB、合计 >20MiB 转受控链接、可执行/脚本附件不支持",
  );

  for (const name of ["setup.EXE", "run.Bat", "task.cmd", "tool.ps1", "app.JS", "v.vbs", "s.scr", "pkg.msi", "dir/sub/tool.ps1"]) {
    assert.equal(mailTask.mailAttachmentUnavailableReason(name), "可执行或脚本附件不支持", name);
  }
  for (const name of ["report.pdf", "notes.exe.txt", "archive.jsx", "readme", ""]) {
    assert.equal(mailTask.mailAttachmentUnavailableReason(name), "", name);
  }

  assert.equal(mailTask.mailAttachmentSelectionError([{name: "report.pdf", size: 25 * MiB}]), "");
  assert.match(
    mailTask.mailAttachmentSelectionError(Array.from({length: 11}, (_, index) => ({name: `a${index}.txt`, size: 1}))),
    /最多 10 个附件/,
  );
  assert.match(
    mailTask.mailAttachmentSelectionError([{name: "big.bin", size: 25 * MiB + 1}]),
    /big\.bin.*超过 25MiB/,
  );
  assert.match(
    mailTask.mailAttachmentSelectionError([{name: "setup.exe", size: 1}]),
    /可执行或脚本/,
  );

  assert.match(mailSource, /data-mail-attachment-limit/, "the mail limit hint is rendered");
  assert.match(mailSource, /MAIL_ATTACHMENT_LIMIT_HINT/, "the rendered hint uses the shared constant");
  assert.match(mailSource, /unavailable \? " disabled" : ""/, "executable or script rows disable their checkbox");
  assert.match(mailSource, /不支持：\$\{escapeHTML\(unavailable\)\}/, "the disabled reason is visible");
  assert.match(mailSource, /attachmentSelectionValid\(\)/, "preview and submit validate the selection");
});
