// PM2 app for the transcode node.
//
//   pm2 start deploy/ecosystem.transcode.config.cjs
//   pm2 save
//
// kill_timeout is the important line. PM2 sends SIGINT and then SIGKILL after
// this long; the node's contract is "finish the file, then exit", so the
// window has to be longer than one film. Ten minutes covers a 1080p feature at
// 8x. Shorter and every `pm2 restart` throws away an encode and increments a
// file's attempt count.
module.exports = {
  apps: [
    {
      name: "transcode-node",
      script: "/Users/jakob/Repos/messer-transcode/bin/messer-transcode",
      args: "node --config /Users/jakob/Server/configs/transcode/config.json --token-file /Users/jakob/Server/configs/transcode/server.token --name mac-mini",
      interpreter: "none", // it is a binary, not a script
      cwd: "/Users/jakob/Repos/messer-transcode",
      autorestart: true,
      max_restarts: 10,
      restart_delay: 5000,
      kill_timeout: 600000,
      time: true,
      out_file: "/Users/jakob/Library/Logs/transcode-node.out.log",
      error_file: "/Users/jakob/Library/Logs/transcode-node.err.log",
      env: {
        NODE_ENV: "production",
      },
    },
  ],
};
