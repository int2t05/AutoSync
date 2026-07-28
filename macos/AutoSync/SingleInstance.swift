// SingleInstance 用 flock 锁 <datadir>/locks/autosync.app.lock 保证单实例。
// 锁在进程退出时自动释放（fd 关闭）；二次启动 acquire 失败即退出。
import Foundation
import Darwin

enum SingleInstanceLock {
    private static var fd: Int32 = -1

    /// 获取排他锁（非阻塞）；已被持返回 false。fd 持有至进程退出。
    static func acquire() -> Bool {
        let dir = appLockDir()
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        let lockPath = (dir as NSString).appendingPathComponent("autosync.app.lock")
        fd = open(lockPath, O_CREAT | O_RDWR, 0o644)
        guard fd >= 0 else { return false }
        return flock(fd, LOCK_EX | LOCK_NB) == 0
    }

    private static func appLockDir() -> String {
        let fm = FileManager.default
        let dir = fm.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        return dir.appendingPathComponent("AutoSync/locks").path
    }
}
