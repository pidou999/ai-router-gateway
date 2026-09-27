const { spawn } = require('child_process');
const path = require('path');

const env = {
  ...process.env,
  JWT_SECRET: 'dev-jwt-secret-change-in-production',
  ENCRYPTION_KEY: 'dev-encryption-key-please-change-32bytes-long',
  DB_TYPE: 'sqlite',
  DB_PATH: path.join(__dirname, 'data/gateway.db'),
  PORT: '5176',
  GIN_MODE: 'release'
};

console.log('Starting gateway...');
const proc = spawn('gateway.exe', [], {
  cwd: __dirname,
  env,
  stdio: 'inherit'
});

proc.on('close', (code) => {
  console.log(`Gateway exited with code ${code}`);
});
