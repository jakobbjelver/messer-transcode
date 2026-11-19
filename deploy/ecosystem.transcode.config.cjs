// PM2 app for the transcode node.
//
//   pm2 start deploy/ecosystem.transcode.config.cjs
//   pm2 save
//
// kill_timeout is the important line. PM2 sends SIGINT and then SIGKILL after
// this long; the node contract is "finish the file, then exit", so the window
// has to be longer than one film. Ten minutes covers a 1080p feature at 8x.
// Shorter and every `pm2 restart` throws away an encode and increments the
// file attempt count.
//
// Every path is derived, never written down: this file is tracked in a public
// repository, and an absolute home directory in it is both a machine fact and
// a leak. PM2 runs as the operator, so os.homedir() answers the path the
// literal used to, and __dirname is wherever the checkout lives.
const os = require("node:os");
const path = require("node:path");

const home = os.homedir();
const repo = path.resolve(__dirname, "..");
const configDir = path.join(home, "Server", "configs", "video", "transcode");
const logDir = path.join(home, "Library", "Logs");

module.exports = {
  apps: [
    {
      name: "transcode-node",
      script: path.join(repo, "bin", "messer-transcode"),
      args: [
        "node",
        "--config", path.join(configDir, "config.json"),
        "--token-file", path.join(configDir, "server.token"),
        "--name", "mac-mini",
      ].join(" "),
      interpreter: "none", // it is a binary, not a script
      cwd: repo,
      autorestart: true,
      max_restarts: 10,
      restart_delay: 5000,
      kill_timeout: 600000,
      time: true,
      out_file: path.join(logDir, "transcode-node.out.log"),
      error_file: path.join(logDir, "transcode-node.err.log"),
      env: {
        NODE_ENV: "production",
      },
    },
  ],
};
