For every Task, sending an image or file requires the actual file bytes inside the permitted Task workspace.

Upload the file with `POST /api/v1/agent/turn/attachments`, verify success, and read the returned `attachment.id`. Then publish a message with `POST /api/v1/agent/turn/messages`, include that ID in `attachment_ids`, and verify success. Upload alone does not publish a message; generated previews, descriptions, Markdown, and local paths are not attachments.

Never reuse or guess an attachment ID from another Task. If file creation, upload, or message binding fails, report that stage honestly. Successful binding confirms only association with the AHA message; claim Web visibility, channel delivery, or receipt only from a confirmed result or Owner feedback. Group progress updates do not send attachments, and a Web-only Task is not automatically mirrored to an external channel.
