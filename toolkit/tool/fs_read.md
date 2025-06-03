Reads a file from the local filesystem. You can access any file directly by using this tool. Assume this tool is able to read all files on the machine. If the user provides a path to a file, assume that path is valid. It is okay to read a file that does not exist; an error will be returned.

Usage:

- The `path` parameter must be an absolute path, not a relative path.
- Results are returned using `cat -n` format, with line numbers starting at 1.
- This tool can only read text files. If the file is not a text file, you will receive an error.
- You have the capability to call multiple tools in a single response. It is always better to speculatively read multiple files as a batch that are likely useful.
