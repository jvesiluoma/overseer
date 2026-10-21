import Crypto
import Foundation
import NIOSSL
import SwiftASN1
import X509

/// The host's self-signed TLS certificate and key.
///
/// Created once and kept on disk so the fingerprint stays stable: browsers remember the
/// exception the user accepted, and the user can compare the fingerprint shown in the menu.
public struct TLSIdentity: Sendable {
    public let certificatePEM: String
    public let privateKeyPEM: String
    /// SHA-256 of the DER certificate, as colon-separated uppercase hex (what browsers display).
    public let fingerprint: String
    public let notValidAfter: Date

    static let certificateFile = "tls-certificate.pem"
    static let keyFile = "tls-key.pem"
    /// Apple rejects TLS server certificates valid for more than 825 days, even self-signed ones.
    static let validity: TimeInterval = 800 * 86_400
    /// Regenerate when less than this is left, so the certificate never expires mid-session.
    static let renewalMargin: TimeInterval = 30 * 86_400

    /// Loads the identity from `directory`, or creates (and saves) a new one if it is missing,
    /// unreadable or close to expiry. Names and addresses only matter for new certificates.
    public static func loadOrCreate(directory: URL, commonName: String, dnsNames: [String], ipAddresses: [String],
                                    now: Date = Date()) throws -> TLSIdentity {
        let certURL = directory.appendingPathComponent(certificateFile)
        let keyURL = directory.appendingPathComponent(keyFile)
        if let certPEM = try? String(contentsOf: certURL, encoding: .utf8),
           let keyPEM = try? String(contentsOf: keyURL, encoding: .utf8),
           let identity = try? TLSIdentity(certificatePEM: certPEM, privateKeyPEM: keyPEM),
           identity.notValidAfter.timeIntervalSince(now) > renewalMargin {
            return identity
        }
        let identity = try generate(commonName: commonName, dnsNames: dnsNames, ipAddresses: ipAddresses, now: now)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true,
                                                attributes: [.posixPermissions: 0o700])
        try write(identity.privateKeyPEM, to: keyURL, permissions: 0o600)
        try write(identity.certificatePEM, to: certURL, permissions: 0o644)
        return identity
    }

    public static func generate(commonName: String, dnsNames: [String], ipAddresses: [String],
                                now: Date = Date()) throws -> TLSIdentity {
        let key = P256.Signing.PrivateKey()
        let name = try DistinguishedName {
            CommonName(commonName)
            OrganizationName("Overscreen")
        }
        var altNames: [GeneralName] = dnsNames.map { .dnsName($0) }
        altNames += ipAddresses.compactMap(ipBytes).map { .ipAddress(ASN1OctetString(contentBytes: ArraySlice($0))) }
        let extensions = try Certificate.Extensions {
            Critical(BasicConstraints.notCertificateAuthority)
            Critical(KeyUsage(digitalSignature: true))
            try ExtendedKeyUsage([.serverAuth])
            SubjectAlternativeNames(altNames)
        }
        let certificate = try Certificate(
            version: .v3,
            serialNumber: Certificate.SerialNumber(),
            publicKey: Certificate.PublicKey(key.publicKey),
            notValidBefore: now.addingTimeInterval(-3600), // tolerate clock skew on the device
            notValidAfter: now.addingTimeInterval(validity),
            issuer: name,
            subject: name,
            signatureAlgorithm: .ecdsaWithSHA256,
            extensions: extensions,
            issuerPrivateKey: Certificate.PrivateKey(key)
        )
        return try TLSIdentity(certificatePEM: certificate.serializeAsPEM().pemString, privateKeyPEM: key.pemRepresentation)
    }

    public init(certificatePEM: String, privateKeyPEM: String) throws {
        let certificate = try Certificate(pemEncoded: certificatePEM)
        let key = try P256.Signing.PrivateKey(pemRepresentation: privateKeyPEM)
        guard Certificate.PublicKey(key.publicKey) == certificate.publicKey else { throw TLSIdentityError.keyMismatch }
        var serializer = DER.Serializer()
        try serializer.serialize(certificate)
        self.certificatePEM = certificatePEM
        self.privateKeyPEM = privateKeyPEM
        self.fingerprint = SHA256.hash(data: serializer.serializedBytes).map { String(format: "%02X", $0) }.joined(separator: ":")
        self.notValidAfter = certificate.notValidAfter
    }

    func makeSSLContext() throws -> NIOSSLContext {
        let certificate = try NIOSSLCertificate(bytes: Array(certificatePEM.utf8), format: .pem)
        let key = try NIOSSLPrivateKey(bytes: Array(privateKeyPEM.utf8), format: .pem)
        var configuration = TLSConfiguration.makeServerConfiguration(certificateChain: [.certificate(certificate)],
                                                                     privateKey: .privateKey(key))
        configuration.minimumTLSVersion = .tlsv12
        return try NIOSSLContext(configuration: configuration)
    }

    private static func ipBytes(_ address: String) -> [UInt8]? {
        var v4 = in_addr()
        if inet_pton(AF_INET, address, &v4) == 1 { return withUnsafeBytes(of: &v4) { Array($0) } }
        var v6 = in6_addr()
        if inet_pton(AF_INET6, address, &v6) == 1 { return withUnsafeBytes(of: &v6) { Array($0) } }
        return nil
    }

    private static func write(_ text: String, to url: URL, permissions: Int) throws {
        try Data(text.utf8).write(to: url, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: permissions], ofItemAtPath: url.path)
    }
}

public enum TLSIdentityError: Error {
    case keyMismatch
}
