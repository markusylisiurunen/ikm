Reads a file from the local filesystem. You can access any file directly by using this tool. Assume this tool is able to read all files on the machine. If the user provides a path to a file, assume that path is valid. It is okay to read a file that does not exist; an error will be returned.

Usage:

- Accepts both absolute and relative paths (relative paths are converted to absolute)
- Results include line numbers in the format: `     1	content` (6-digit line number + tab + content)
- This tool can only read text files up to 10MB. If the file is not a text file or exceeds the size limit, you will receive an error
- Supports optional `offset` and `limit` parameters for reading large files in chunks (1-based line numbering)
- You have the capability to call multiple tools in a single response. It is ALWAYS better to speculatively read multiple files as a batch that are likely useful
