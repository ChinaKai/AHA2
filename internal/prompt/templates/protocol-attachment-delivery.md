Attachments: they require the actual file bytes inside the permitted Task workspace, and only a confirmed upload plus binding to a message counts as delivered; mentioning a path, or uploading without binding, does not. Never claim delivery before it is confirmed, and never reuse an attachment ID from another Task.

Read the `attachment-protocol.md` entry in the Available context list for the upload, binding, and receipt procedure before sending an image or file: file bytes are uploaded over HTTP only, and binding them to a message can use the `attachment_bind` MCP tool.
